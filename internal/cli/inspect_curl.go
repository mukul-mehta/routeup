package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/route"
)

func writeInspectCurl(out io.Writer, entry logs.Entry, tlsPort int) error {
	if entry.Capture == nil || entry.Capture.Request == nil {
		return fmt.Errorf("request %s has no captured request", terminalEscapeString(entry.ID))
	}
	request := entry.Capture.Request
	if !request.Complete {
		return fmt.Errorf("request %s capture is incomplete; cannot create curl command", terminalEscapeString(entry.ID))
	}
	if !validHTTPToken(entry.Method) {
		return fmt.Errorf("request %s has an invalid method", terminalEscapeString(entry.ID))
	}
	targetURL, err := inspectCurlURL(entry, tlsPort)
	if err != nil {
		return err
	}
	headers, err := inspectCurlHeaders(request)
	if err != nil {
		return fmt.Errorf("request %s: %w", terminalEscapeString(entry.ID), err)
	}

	binaryBody := len(request.Body) > 0 && !directCurlBody(request.Body)
	if binaryBody {
		encoded := base64.StdEncoding.EncodeToString(request.Body)
		if _, err := fmt.Fprintf(out, "printf '%%s' %s | base64 -d | ", shellQuote(encoded)); err != nil {
			return fmt.Errorf("write curl body pipeline: %w", err)
		}
	}
	if _, err := io.WriteString(out, "curl"); err != nil {
		return fmt.Errorf("write curl command: %w", err)
	}
	if err := writeCurlFlag(out, "--ipv4"); err != nil {
		return err
	}
	if err := writeCurlFlag(out, "--path-as-is"); err != nil {
		return err
	}
	if err := writeCurlOption(out, "--request", entry.Method); err != nil {
		return err
	}
	for _, header := range headers {
		if err := writeCurlOption(out, "--header", header); err != nil {
			return err
		}
	}
	if len(request.Body) > 0 {
		if request.Headers.Get("Content-Type") == "" {
			if err := writeCurlOption(out, "--header", "Content-Type:"); err != nil {
				return err
			}
		}
		if binaryBody {
			if err := writeCurlOption(out, "--data-binary", "@-"); err != nil {
				return err
			}
		} else {
			if err := writeCurlOption(out, "--data-raw", string(request.Body)); err != nil {
				return err
			}
		}
	}
	if err := writeCurlOption(out, "--url", targetURL); err != nil {
		return err
	}
	if _, err := io.WriteString(out, "\n"); err != nil {
		return fmt.Errorf("finish curl command: %w", err)
	}
	return nil
}

func directCurlBody(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	for _, r := range string(body) {
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		return false
	}
	return true
}

func inspectCurlURL(entry logs.Entry, tlsPort int) (string, error) {
	name, err := route.Parse(entry.Route)
	if err != nil {
		return "", fmt.Errorf("request %s has an invalid route: %w", terminalEscapeString(entry.ID), err)
	}
	parsed, err := url.ParseRequestURI(entry.RequestPath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(entry.RequestPath, "/") || strings.Contains(entry.RequestPath, "#") {
		return "", fmt.Errorf("request %s has an invalid request path", terminalEscapeString(entry.ID))
	}
	return localURL(name.LocalHost(), tlsPort) + entry.RequestPath, nil
}

func inspectCurlHeaders(request *logs.CapturedMessage) ([]string, error) {
	keys := make([]string, 0, len(request.Headers))
	for name := range request.Headers {
		keys = append(keys, name)
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
	})

	var headers []string
	for _, name := range keys {
		if !validHTTPToken(name) {
			return nil, fmt.Errorf("captured header has invalid name %q", terminalEscapeString(name))
		}
		for _, value := range request.Headers[name] {
			if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
				return nil, fmt.Errorf("captured header %q has a control character", terminalEscapeString(name))
			}
			if value == "" {
				headers = append(headers, name+";")
			} else {
				headers = append(headers, name+": "+value)
			}
		}
	}
	return headers, nil
}

func writeCurlFlag(out io.Writer, name string) error {
	if _, err := fmt.Fprintf(out, " \\\n  %s", name); err != nil {
		return fmt.Errorf("write curl flag %s: %w", name, err)
	}
	return nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c <= 0x20 || c >= 0x7f || strings.ContainsRune(`()<>@,;:\"/[]?={}`, rune(c)) {
			return false
		}
	}
	return true
}

func writeCurlOption(out io.Writer, name, value string) error {
	if _, err := fmt.Fprintf(out, " \\\n  %s %s", name, shellQuote(value)); err != nil {
		return fmt.Errorf("write curl option %s: %w", name, err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
