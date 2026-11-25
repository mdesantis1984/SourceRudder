package connectors

import (
	"context"
	"io"
	"net/http"
	"time"
)

// retryBackoff is the pause before the single retry of a flaky request. It is
// a variable only so tests can shorten it.
var retryBackoff = 500 * time.Millisecond

// retryableStatus reports the statuses worth one more try: rate limiting and
// server errors. Other 4xx answers are final.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// doWithRetry sends a body-less request and retries it once, after
// retryBackoff, on a network error, 429 or 5xx. The wait ends early when ctx
// does, and no retry is made once ctx is done. The caller closes the returned
// response body.
func doWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if ctx.Err() != nil {
		return resp, err
	}
	if err == nil && !retryableStatus(resp.StatusCode) {
		return resp, nil
	}
	if resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}
	timer := time.NewTimer(retryBackoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	return client.Do(req.Clone(ctx))
}
