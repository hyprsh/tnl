package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// PathBackup streams the backup archive (a tar). GET, local only.
const PathBackup = "/v1/backup"

// SkippedHeader lists, one value per certificate, the certificates left out of
// the archive because their key doesn't match. It is sent before the body.
const SkippedHeader = "Tnl-Backup-Skipped"

// Download sends GET path and returns the body as a stream, for answers that
// aren't JSON. The same version check and refusals as Call apply. The caller
// closes the body.
func (c *Client) Download(ctx context.Context, path string) (io.ReadCloser, http.Header, error) {
	if err := c.checkLocal(ctx); err != nil {
		return nil, nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://tnl"+path, nil)
	if err != nil {
		return nil, nil, err
	}
	hreq.Header.Set(VersionHeader, c.Version)
	resp, err := c.http().Do(hreq)
	if err != nil {
		return nil, nil, c.dialError(err)
	}
	if resp.Header.Get(VersionHeader) == "" {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("%s answered without a %s header; it is not a tnl", c.socket(), VersionHeader)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, nil, err
		}
		var eb errorBody
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		return nil, nil, &Error{Status: resp.StatusCode, Message: msg}
	}
	return resp.Body, resp.Header, nil
}
