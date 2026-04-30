package httpcommon

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
)

// ScanSSE walks an `text/event-stream` body line by line, invoking
// onData with the payload of each `data: <payload>` frame. Empty lines
// and non-data lines (e.g. `event: …`, comments) are ignored. Returning
// true from onData terminates the loop early — used by the OpenAI
// stream to stop on `[DONE]` and by the Hermes stream to stop on
// `response.completed`.
//
// Stream errors are returned, except context cancellation which is
// treated as a clean shutdown (the caller already handles abort).
//
// The buffer is sized for individual SSE lines up to 1 MiB — large
// enough for any single chat completion chunk we've seen in practice.
func ScanSSE(r io.Reader, onData func(payload string) (stop bool)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if onData(payload) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
