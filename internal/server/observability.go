package server

import (
	"bufio"
	"net"
	"net/http"
)

type observedResponseWriter struct {
	http.ResponseWriter
	statusCode int
	bytes      int64
	hijacked   bool
}

func (w *observedResponseWriter) WriteHeader(statusCode int) {
	if statusCode >= 100 && statusCode < 200 && statusCode != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(statusCode)
		return
	}
	if w.statusCode != 0 {
		return
	}
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *observedResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *observedResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *observedResponseWriter) Flush() {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *observedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}

func (w *observedResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *observedResponseWriter) status() int {
	if w.statusCode != 0 {
		return w.statusCode
	}
	if w.hijacked {
		return http.StatusSwitchingProtocols
	}
	return http.StatusOK
}
