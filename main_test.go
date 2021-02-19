package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/gavv/httpexpect"
)

func init() {
	log.SetOutput(ioutil.Discard)
}

func TestServer(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	proxy, _ := net.Listen("tcp", "127.0.0.1:")
	proxyAddr := proxy.Addr().String()
	{
		go func() {
			c, _ := proxy.Accept()
			c.SetDeadline(time.Now().Add(time.Second))
			req, _ := http.ReadRequest(bufio.NewReader(c))
			if http.MethodConnect != req.Method {
				_, file, line, _ := runtime.Caller(0)
				t.Logf("%s:%d:\n\n\texp: %#v\n\n\tgot: %#v\n\n",
					filepath.Base(file),
					line,
					http.MethodConnect,
					req.Method)
				t.FailNow()
			}
			t.Log(req.Method)
			var buf bytes.Buffer
			(&http.Response{
				StatusCode: http.StatusOK,
				Body:       ioutil.NopCloser(&buf),
			}).Write(c)
		}()
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "OK")
	}))
	defer ts.Close()

	svr := httptest.NewServer(Run(time.Millisecond * 5))
	defer svr.Close()

	e := httpexpect.New(t, svr.URL)
	e.GET("").WithQuery("proxy", proxyAddr).Expect()
	t.Run("plain check", func(t *testing.T) {
		e.GET("/"+ts.Listener.Addr().String()).
			Expect().
			StatusRange(httpexpect.Status2xx).
			JSON().Object().
			ValueEqual("status", "OK").
			NotContainsKey("proxy")
	})

	t.Run("invalid host", func(t *testing.T) {
		e.GET("/xyz").
			Expect().
			StatusRange(httpexpect.Status4xx).
			JSON().Object().
			ValueEqual("status", "INVALID_HOST")
	})

	t.Run("unreachable host", func(t *testing.T) {
		e.GET("/127.0.0.1").
			Expect().
			StatusRange(httpexpect.Status4xx).
			JSON().Object().
			ValueEqual("status", "INVALID_HOST")
	})

	t.Run("host unreachable", func(t *testing.T) {
		e.GET("/127.0.0.1:1").
			Expect().
			StatusRange(httpexpect.Status5xx).
			JSON().Object().
			ValueEqual("status", "HOST_CONNECT_FAIL")
	})

	t.Run("bad proxy", func(t *testing.T) {
		e.GET("/"+ts.Listener.Addr().String()).
			WithQuery("proxy", "abc").
			Expect().
			StatusRange(httpexpect.Status4xx).
			JSON().Object().
			ContainsMap(map[string]interface{}{
				"error":  "dial tcp: address abc: missing port in address",
				"status": "PROXY_UNREACHABLE",
				"proxy":  "abc",
			})
	})

	t.Run("connect via proxy", func(t *testing.T) {
		e.GET("/"+ts.Listener.Addr().String()).
			WithQuery("proxy", proxyAddr).
			Expect().
			StatusRange(httpexpect.Status2xx).
			JSON().Object().
			ContainsMap(map[string]interface{}{
				"status": "OK",
				"proxy":  proxyAddr,
			})
	})

	t.Run("proxy times out", func(t *testing.T) {
		svr := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Second / 100)
			}))
		defer svr.Close()
		clsr := svr.Listener
		e.GET("/"+ts.Listener.Addr().String()).
			WithQuery("proxy", clsr.Addr().String()).
			Expect().
			StatusRange(httpexpect.Status5xx).
			JSON().Object().
			ContainsMap(map[string]interface{}{
				"status": "PROXY_CONNECT_ERROR",
				"proxy":  clsr.Addr().String(),
			})
	})

	t.Run("proxy does not connect", func(t *testing.T) {
		e.GET("/127.0.0.1:1").
			WithQuery("proxy", proxyAddr).
			Expect().
			StatusRange(httpexpect.Status5xx).
			JSON().Object().
			ContainsMap(map[string]interface{}{
				"status": "PROXY_CONNECT_ERROR",
				"proxy":  proxyAddr,
			})
	})

}
func TestProxy(t *testing.T) {
	proxy, _ := net.Listen("tcp", "127.0.0.1:")
	proxyAddr := proxy.Addr().String()
	go func() {
		c, _ := proxy.Accept()
		c.SetDeadline(time.Now().Add(time.Second))
		req, _ := http.ReadRequest(bufio.NewReader(c))
		if http.MethodConnect != req.Method {
			_, file, line, _ := runtime.Caller(0)
			t.Logf("%s:%d:\n\n\texp: %#v\n\n\tgot: %#v\n\n",
				filepath.Base(file),
				line,
				http.MethodConnect,
				req.Method)
			t.FailNow()
		}
		t.Log(req.Method)
		var buf bytes.Buffer
		(&http.Response{
			StatusCode: http.StatusOK,
			Body:       ioutil.NopCloser(&buf),
		}).Write(c)
	}()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "OK")
	}))
	defer ts.Close()

	var handler http.Handler = proxyHandler{Timeout: 1 * time.Second}
	res := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/google.com:80", nil)
	req.URL.RawQuery = url.Values{
		"proxy": {proxyAddr},
	}.Encode()

	handler.ServeHTTP(res, req)
	expected := http.StatusOK
	actual := res.Code
	if !reflect.DeepEqual(expected, actual) {
		_, file, line, _ := runtime.Caller(0)
		fmt.Printf("%s:%d:\n\n\texp: %#v\n\n\tgot: %#v\n\n", filepath.Base(file), line, expected, actual)
		t.FailNow()
	}

}
