package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	portFlag      = flag.String("port", ":8989", "port to listen on")
	lookupFlag    = flag.String("lookup", "./lookup", "lookup file")
	cacheTimeFlag = flag.Int64("cacheTime", 5, "Number of minutes to cache thumbnail")
)

func main() {
	flag.Parse()
	cache := &cache{
		t: time.Duration(*cacheTimeFlag),
		c: make(map[string]struct {
			d time.Time
			b []byte
		}),
	}

	t := thumbnailer{
		c: cache,
	}
	lookupdata := readLookupData(*lookupFlag)
	lookupFunc := func(name string) (string, error) {
		if v, ok := lookupdata[name]; ok {
			return v, nil
		}
		return "", errors.New("invalid camera name")
	}

	var keys []string
	for k := range lookupdata {
		keys = append(keys, k)
	}
	go func() {
		t.refresh(keys...)
		c := time.Tick(time.Duration(*cacheTimeFlag) * time.Minute)
		for range c {
			t.refresh(keys...)
		}
	}()

	http.HandleFunc("/cam/thumb/", thumbnail(&t, lookupFunc))
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

func thumbnail(t *thumbnailer, lookup func(string) (string, error)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/cam/thumb/"):]
		cam, err := lookup(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		img := t.thumbnail(cam)
		if img == nil {
			http.Error(w, "invalid camera feed", http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "image/png")
		w.Header().Add("Content-Length", strconv.Itoa(len(img)))
		w.Write(img)
	}
}

type thumbnailer struct {
	mu sync.Mutex
	c  *cache
}

func (t *thumbnailer) thumbnail(key string) []byte {
	return t.c.getOrAdd(key, func() ([]byte, error) {
		uri, err := url.Parse(key)
		if err != nil {
			return nil, err
		}
		return t.generateThumbnail(uri)
	})
}

func (t *thumbnailer) refresh(keys ...string) {
	for _, key := range keys {
		uri, err := url.Parse(key)
		if err != nil {
			continue
		}
		thumb, err := t.generateThumbnail(uri)
		if err != nil {
			continue
		}

		t.mu.Lock()
		t.c.set(key, thumb)
		t.mu.Unlock()
	}
}

func (t *thumbnailer) generateThumbnail(uri *url.URL) ([]byte, error) {
	prog := "ffmpeg"
	args := []string{
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
	t time.Duration
	c map[string]struct {
		d time.Time
		b []byte
	}
}

func (c *cache) set(url string, thumb []byte) {
	c.c[url] = struct {
		d time.Time
		b []byte
	}{time.Now(), thumb}
}

func (c *cache) getOrAdd(url string, imageFunc func() ([]byte, error)) []byte {
	old := func(current time.Time) bool {
		diff := time.Since(current)
		return diff > (c.t * time.Minute)
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
		return nil
	}
	c.c[url] = struct {
		d time.Time
		b []byte
	}{time.Now(), b}
	return b
}
