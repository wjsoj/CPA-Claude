package auth

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
)

// applyAxiosOAuthHeaders sets the request headers exactly as captured from
// real Claude Code 2.1.126 talking to platform.claude.com / api.anthropic.com
// during the OAuth token-exchange and refresh requests:
//
//	User-Agent: axios/1.13.6
//	Accept: application/json, text/plain, */*
//	Accept-Encoding: gzip, br
//	Connection: close
//	Content-Type: application/json   (when there is a body)
//
// Header insertion order is fixed by Go's textproto map and not the wire
// order, but the set of headers and their values matches byte-for-byte.
func applyAxiosOAuthHeaders(req *http.Request, hasBody bool) {
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Encoding", "gzip, br")
	req.Header.Set("User-Agent", anthropicOAuthUA)
	req.Header.Set("Connection", "close")
}

// readAxiosOAuthBody fully consumes the response body, transparently
// decoding gzip / brotli when the server compresses (which it does, because
// we advertised Accept-Encoding above and Go's http.Transport disables
// auto-decompression as soon as the caller sets that header manually).
func readAxiosOAuthBody(resp *http.Response) ([]byte, error) {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	r := io.Reader(resp.Body)
	switch enc {
	case "", "identity":
		// no-op
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer zr.Close()
		r = zr
	case "br":
		r = brotli.NewReader(resp.Body)
	default:
		return nil, fmt.Errorf("unexpected Content-Encoding %q", enc)
	}
	return io.ReadAll(r)
}

// doAxiosOAuthRequest builds and executes an axios-style OAuth request to
// platform.claude.com. body is the already-marshalled JSON payload (or nil
// for a GET / empty POST). Returns the HTTP response together with its
// decoded body so callers don't have to repeat the gzip/br dance.
//
// Errors are returned verbatim — caller decides how to map status codes
// onto MarkFailure / MarkHardFailure semantics.
func doAxiosOAuthRequest(
	ctx context.Context,
	client *http.Client,
	method, urlStr string,
	body []byte,
) (*http.Response, []byte, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, reader)
	if err != nil {
		return nil, nil, err
	}
	applyAxiosOAuthHeaders(req, len(body) > 0)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	data, derr := readAxiosOAuthBody(resp)
	_ = resp.Body.Close()
	if derr != nil {
		return resp, nil, derr
	}
	return resp, data, nil
}
