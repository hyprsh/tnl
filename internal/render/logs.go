package render

import (
	"bufio"
	"context"
	"io"

	"github.com/hyprsh/tnl/internal/api"
)

// Logs asks c for the log lines q describes and copies them to w as they
// come, flushing after each chunk so a followed line shows at once. It returns
// nil when the reply ends or ctx does. It is what `tnl logs` runs once the
// query is checked and a process found.
func Logs(ctx context.Context, w io.Writer, c *api.Client, q api.LogsQuery) error {
	body, _, err := c.Download(ctx, api.PathLogs+"?"+q.Values().Encode())
	if err != nil {
		return err
	}
	defer body.Close()
	bw := bufio.NewWriter(w)
	buf := make([]byte, 32<<10)
	for {
		m, rerr := body.Read(buf)
		bw.Write(buf[:m])
		bw.Flush()
		if rerr == io.EOF || ctx.Err() != nil {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
