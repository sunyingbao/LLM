package handler

import (
	"context"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"code.byted.org/middleware/hertz/pkg/app"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/webui"
)

func WebApp(ctx context.Context, c *app.RequestContext) {
	serveWebFile(c, "index.html")
}

func WebAsset(ctx context.Context, c *app.RequestContext) {
	name := strings.TrimPrefix(string(c.Path()), "/static/")
	serveWebFile(c, name)
}

func WebRootAsset(ctx context.Context, c *app.RequestContext) {
	name := strings.TrimPrefix(string(c.Path()), "/")
	serveWebFile(c, name)
}

func Favicon(ctx context.Context, c *app.RequestContext) {
	serveWebFile(c, "favicon.svg")
}

func serveWebFile(c *app.RequestContext, name string) {
	clean := path.Clean("/" + strings.TrimSpace(name))
	if clean == "/" || strings.Contains(clean, "..") {
		clean = "/index.html"
	}
	filePath := "static" + clean
	data, err := fs.ReadFile(webui.Static, filePath)
	if err != nil {
		c.Response.SetStatusCode(http.StatusNotFound)
		c.Response.SetBody([]byte("not found"))
		return
	}
	contentType := mime.TypeByExtension(path.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Response.SetStatusCode(http.StatusOK)
	c.Response.Header.SetContentType(contentType)
	c.Response.SetBody(data)
}
