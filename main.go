package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	portFlag   = flag.String("port", ":8989", "port to listen on")
	lookupFlag = flag.String("lookup", "./lookup", "lookup file")
)

func main() {
	flag.Parse()
	cache := &cache{
		c: make(map[string]struct {
			d time.Time
			b []byte
		}),
	}

	lookupdata := readLookupData(*lookupFlag)
	lookupFunc := func(name string) (string, error) {
		if v, ok := lookupdata[name]; ok {
			return v, nil
		}
		return "", errors.New("invalid camera name")
	}

	http.HandleFunc("/cam/thumb/", thumbnail(cache, lookupFunc))
	http.ListenAndServe(*portFlag, nil)
}

func readLookupData(file string) map[string]string {
	data, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}

	text := strings.Fields(string(data))
	lookupdata := make(map[string]string)
	for ; len(text) > 0; text = text[2:] {
		lookupdata[text[0]] = text[1]
	}
	return lookupdata
}

func thumbnail(cache *cache, lookup func(string) (string, error)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/cam/thumb/"):]
		cam, err := lookup(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		img := cache.getOrAdd(cam, func() ([]byte, error) {
			uri, err := url.Parse(cam)
			if err != nil {
				return nil, err
			}
			return generateThumbnail(uri)
		})
		if img == nil {
			http.Error(w, "invalid camera feed", http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "image/png")
		w.Header().Add("Content-Length", strconv.Itoa(len(img)))
		w.Write(img)
	}
}

func generateThumbnail(uri *url.URL) ([]byte, error) {
	prog := "ffmpeg"
	args := []string{
		// "-seekable", "1",
		"-i", uri.String(),
		"-vf", "thumbnail",
		"-frames:v", "1",
		"-f", "image2pipe",
		"-c:v", "png",
		"pipe:1",
	}

	cmd := exec.Command(prog, args...)

	var buf bytes.Buffer
	cmd.Stdout = bufio.NewWriter(&buf)

	err := cmd.Run()
	return buf.Bytes(), err
}

type cache struct {
	c map[string]struct {
		d time.Time
		b []byte
	}
}

func (c *cache) getOrAdd(url string, imageFunc func() ([]byte, error)) []byte {
	old := func(current time.Time) bool {
		diff := time.Since(current)
		return diff > (1 * time.Minute)
	}
	if v, ok := c.c[url]; ok {
		if old(v.d) {
			delete(c.c, url)
		} else {
			return v.b
		}
	}
	b, err := imageFunc()
	if err != nil {
		fmt.Printf(err.Error())
		return nil
	}
	c.c[url] = struct {
		d time.Time
		b []byte
	}{time.Now(), b}
	return b
}
