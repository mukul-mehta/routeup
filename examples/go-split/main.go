package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	frontendAddr = "127.0.0.1:5173"
	apiAddr      = "127.0.0.1:8080"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	frontend := &http.Server{
		Addr:              frontendAddr,
		Handler:           frontendHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	api := &http.Server{
		Addr:              apiAddr,
		Handler:           apiHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go serve("frontend", frontend, errCh)
	go serve("api", api, errCh)

	log.Printf("frontend listening on http://%s", frontendAddr)
	log.Printf("api listening on http://%s", apiAddr)
	log.Printf("run `../../routeup serve` in this directory, then open https://go-split.localhost")

	select {
	case <-ctx.Done():
	case err := <-errCh:
		log.Printf("server error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = frontend.Shutdown(shutdownCtx)
	}()
	go func() {
		defer wg.Done()
		_ = api.Shutdown(shutdownCtx)
	}()
	wg.Wait()
}

func serve(name string, srv *http.Server, errCh chan<- error) {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("%s: %w", name, err)
	}
}

func frontendHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML("Go", "go-split")))
	})
	return mux
}

func apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"language": "go",
			"message":  "hello from the API target",
			"host":     r.Host,
			"path":     r.URL.Path,
		})
	})
	mux.HandleFunc("POST /api/webhooks/demo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "received", "path": r.URL.Path})
	})
	return mux
}

func indexHTML(language, routeName string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>routeup %s example</title>
    <style>
      body { margin: 0; font-family: system-ui, sans-serif; background: #101828; color: #f8fafc; }
      main { max-width: 720px; margin: 10vh auto; padding: 32px; }
      code, pre { background: #1e293b; border-radius: 8px; padding: 2px 6px; }
      pre { padding: 16px; overflow: auto; }
      button { border: 0; border-radius: 999px; padding: 10px 16px; font-weight: 700; cursor: pointer; }
      .card { background: #172033; border: 1px solid #334155; border-radius: 18px; padding: 24px; }
    </style>
  </head>
  <body>
    <main>
      <div class="card">
        <p>%s frontend target</p>
        <h1>https://%s.localhost</h1>
        <p>This page is served by the frontend target. The button calls <code>/api/message</code>, which routeup sends to the API target.</p>
        <button id="load">Call API target</button>
        <pre id="output">Waiting...</pre>
      </div>
    </main>
    <script>
      document.querySelector("#load").addEventListener("click", async () => {
        const res = await fetch("/api/message");
        document.querySelector("#output").textContent = JSON.stringify(await res.json(), null, 2);
      });
    </script>
  </body>
</html>
`, language, language, routeName)
}
