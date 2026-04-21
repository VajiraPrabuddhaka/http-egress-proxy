package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// ProxyRequest is the incoming request specifying what HTTP call to make.
type ProxyRequest struct {
	// URL is the target endpoint to call
	URL string `json:"url"`
	// Method is the HTTP method (GET, POST, PUT, DELETE, etc.)
	Method string `json:"method"`
	// Headers to send with the request
	Headers map[string]string `json:"headers,omitempty"`
	// Body to send (for POST/PUT/PATCH)
	Body string `json:"body,omitempty"`
	// Timeout in seconds (default: 10)
	Timeout int `json:"timeout,omitempty"`
}

// ProxyResponse is returned to the caller with the external call result.
type ProxyResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Duration   string            `json:"duration"`
	Error      string            `json:"error,omitempty"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReady)
	mux.HandleFunc("POST /proxy", makeProxyHandler(logger))

	addr := fmt.Sprintf(":%s", port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("starting http-egress-proxy", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "error", err)
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

func handleReady(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ready")
}

func makeProxyHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ProxyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, ProxyResponse{Error: "invalid request body: " + err.Error()})
			return
		}

		if req.URL == "" {
			writeJSON(w, http.StatusBadRequest, ProxyResponse{Error: "url is required"})
			return
		}
		if req.Method == "" {
			req.Method = "GET"
		}
		req.Method = strings.ToUpper(req.Method)

		timeout := 10 * time.Second
		if req.Timeout > 0 && req.Timeout <= 60 {
			timeout = time.Duration(req.Timeout) * time.Second
		}

		logger.Info("proxying request", "method", req.Method, "url", req.URL)

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		var body io.Reader
		if req.Body != "" {
			body = strings.NewReader(req.Body)
		}

		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ProxyResponse{Error: "invalid request: " + err.Error()})
			return
		}

		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}

		start := time.Now()
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(httpReq)
		duration := time.Since(start)

		if err != nil {
			logger.Warn("proxy request failed", "url", req.URL, "error", err, "duration", duration)
			writeJSON(w, http.StatusOK, ProxyResponse{
				Error:    "egress request failed: " + err.Error(),
				Duration: duration.String(),
			})
			return
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
		if err != nil {
			writeJSON(w, http.StatusOK, ProxyResponse{
				Error:    "failed to read response body: " + err.Error(),
				Duration: duration.String(),
			})
			return
		}

		respHeaders := make(map[string]string)
		for k := range resp.Header {
			respHeaders[k] = resp.Header.Get(k)
		}

		logger.Info("proxy request completed",
			"url", req.URL,
			"status", resp.StatusCode,
			"duration", duration,
			"response_size", len(respBody),
		)

		writeJSON(w, http.StatusOK, ProxyResponse{
			StatusCode: resp.StatusCode,
			Headers:    respHeaders,
			Body:       string(respBody),
			Duration:   duration.String(),
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}