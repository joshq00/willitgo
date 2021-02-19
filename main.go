package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

type result struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Proxy  string `json:"proxy,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("content-type", "application/json;charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func Run(timeout time.Duration) http.Handler {
	// timeout := time.Second * 5
	withProxy := proxyHandler{Timeout: timeout}
	checker := plainTest{
		Dialer: net.Dialer{
			KeepAlive: 0,
			Timeout:   timeout},
	}
	plain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(status string, code int) {
			writeJSON(w, code, result{
				Status: status,
			})
		}
		host, port, err := net.SplitHostPort(r.URL.Path[1:])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, result{
				Status: "INVALID_HOST",
				Error:  err.Error(),
			})
			return
		}
		if err := checker.Check(host, port); err != nil {
			writeJSON(w, http.StatusBadGateway, result{
				Status: "HOST_CONNECT_FAIL",
				Error:  err.Error(),
			})
			return
		}
		write("OK", http.StatusOK)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func(start time.Time) {
			log.Println(r.URL.Path[1:], r.URL.Query().Get("proxy"), time.Since(start).String())
		}(time.Now())

		var h http.Handler
		if r.URL.Query().Get("proxy") != "" {
			h = withProxy
		} else {
			h = plain
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	log.Println(http.ListenAndServe(":8080", Run(time.Second*5)))
}

type plainTest struct {
	net.Dialer
}

func (t plainTest) Check(host, port string) error {
	c, err := t.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

type proxyTest struct {
	net.Dialer
	ProxyURL url.URL
}

type proxyHandler struct {
	// net.Dialer
	Timeout time.Duration
}

func (p proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxy := r.URL.Query().Get("proxy")
	host, port, err := net.SplitHostPort(r.URL.Path[1:])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, result{
			Status: "BAD_URL",
			Error:  err.Error(),
			Proxy:  proxy,
		})
		return
	}
	dialer := net.Dialer{Timeout: p.Timeout, KeepAlive: 0}
	c, err := dialer.Dial("tcp", proxy)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, result{
			Status: "PROXY_UNREACHABLE",
			Error:  err.Error(),
			Proxy:  proxy,
		})
		return
	}
	defer c.Close()
	if p.Timeout > 0 {
		_ = c.SetDeadline(time.Now().Add(p.Timeout))
	}

	fmt.Fprintf(c, "CONNECT %s:%s HTTP/1.1\n\n", host, port)
	res, err := http.ReadResponse(bufio.NewReader(c), nil)

	reslt := result{
		Status: "OK",
		Proxy:  proxy,
	}
	if err != nil {
		log.Println(err, "host", host, "port", port, "proxy", proxy)

		var status int
		// status = http.StatusInternalServerError
		status = http.StatusGatewayTimeout
		reslt.Status = "PROXY_CONNECT_ERROR"
		reslt.Error = err.Error()

		switch err := err.(type) {
		case net.Error:
			{
				status = http.StatusServiceUnavailable
				reslt.Status = "HOST_CONNECT_FAIL"
				if err.Timeout() {
					status = http.StatusGatewayTimeout
					reslt.Status = "PROXY_CONNECT_ERROR"
					log.Println(err)
				}
				reslt.Error = fmt.Errorf("net error: %v", err).Error()
			}
		default:
		}

		writeJSON(w, status, reslt)
		return
	}
	go func() {
		io.Copy(ioutil.Discard, res.Body)
		res.Body.Close()
	}()

	for k, vals := range res.Header {
		for _, v := range vals {
			w.Header().Set(k, v)
		}
		w.Header().Del("content-length")
	}
	writeJSON(w, res.StatusCode, reslt)
}