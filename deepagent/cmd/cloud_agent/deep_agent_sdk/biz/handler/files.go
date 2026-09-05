package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"code.byted.org/middleware/hertz/pkg/app"
	"code.byted.org/middleware/hertz_ext/v2/binding"
	httpbase "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/base"
	servicecommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
	filesvc "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/files"
)

type listFilesResponse struct {
	Files    []*filesvc.FileInfo `json:"files"`
	BaseResp *httpbase.BaseResp  `json:"BaseResp"`
}

func ListFiles(ctx context.Context, c *app.RequestContext) {
	var req filesvc.ListRequest
	if err := binding.BindAndValidate(c, &req); err != nil {
		writeFilesError(c, err)
		return
	}
	ctx, uid, err := currentUID(ctx, c)
	if err != nil {
		writeFilesError(c, err)
		return
	}
	resp, err := filesvc.List(ctx, uid, &req)
	if err != nil {
		writeFilesError(c, err)
		return
	}
	c.JSON(http.StatusOK, &listFilesResponse{Files: resp.Files, BaseResp: servicecommon.BaseRespOK()})
}

func ServeFile(ctx context.Context, c *app.RequestContext) {
	sessionID, err := strconv.ParseInt(string(c.QueryArgs().Peek("session_id")), 10, 64)
	if err != nil || sessionID == 0 {
		writeFilePlainError(c, servicecommon.InvalidArgument("session_id is required"))
		return
	}
	rawPath := string(c.QueryArgs().Peek("path"))
	ctx, uid, err := currentUID(ctx, c)
	if err != nil {
		writeFilePlainError(c, err)
		return
	}
	rangeHeader := string(c.Request.Header.Peek("Range"))
	if rangeHeader != "" {
		if rangeFile, err := filesvc.ResolveRangeFile(ctx, uid, sessionID, rawPath); err == nil {
			serveRangeFile(c, rangeFile, rangeHeader)
			return
		}
	}
	file, err := filesvc.ResolveFile(ctx, uid, sessionID, rawPath)
	if err != nil {
		writeFilePlainError(c, err)
		return
	}
	contentType := file.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Response.Header.Set("Accept-Ranges", "bytes")
	if rangeHeader != "" {
		start, end, err := parseHTTPRange(rangeHeader, file.Size)
		if err != nil {
			writeRangeError(c, file.Size)
			return
		}
		serveFileBytes(c, contentType, file.Size, start, end, file.Content[start:end+1])
		return
	}
	c.Response.SetStatusCode(http.StatusOK)
	c.Response.Header.SetContentType(contentType)
	c.Response.Header.Set("Cache-Control", "private, max-age=60")
	c.Response.SetBody(file.Content)
}

func serveRangeFile(c *app.RequestContext, file *filesvc.RangeFile, rangeHeader string) {
	contentType := file.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	start, end, err := parseHTTPRange(rangeHeader, file.Size)
	if err != nil {
		writeRangeError(c, file.Size)
		return
	}
	content, err := file.ReadRange(start, end)
	if err != nil {
		writeFilePlainError(c, err)
		return
	}
	serveFileBytes(c, contentType, file.Size, start, end, content)
}

func serveFileBytes(c *app.RequestContext, contentType string, size, start, end int64, content []byte) {
	c.Response.SetStatusCode(http.StatusPartialContent)
	c.Response.Header.SetContentType(contentType)
	c.Response.Header.Set("Accept-Ranges", "bytes")
	c.Response.Header.Set("Cache-Control", "private, max-age=60")
	c.Response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	c.Response.SetBody(content)
}

func parseHTTPRange(raw string, size int64) (int64, int64, error) {
	if size <= 0 || size > int64(int(^uint(0)>>1)) {
		return 0, 0, fmt.Errorf("invalid content size %d", size)
	}
	start, end, err := app.ParseByteRange([]byte(raw), int(size))
	if err != nil {
		return 0, 0, err
	}
	return int64(start), int64(end), nil
}

func writeRangeError(c *app.RequestContext, size int64) {
	c.Response.SetStatusCode(http.StatusRequestedRangeNotSatisfiable)
	c.Response.Header.Set("Accept-Ranges", "bytes")
	c.Response.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
	c.Response.Header.SetContentType("text/plain; charset=utf-8")
	c.Response.SetBody([]byte("Range Not Satisfiable"))
}

func writeFilesError(c *app.RequestContext, err error) {
	baseResp, status := commonBaseResp(err)
	c.JSON(status, &listFilesResponse{BaseResp: baseResp})
}

func writeFilePlainError(c *app.RequestContext, err error) {
	baseResp, status := commonBaseResp(err)
	c.Response.SetStatusCode(status)
	c.Response.Header.SetContentType("text/plain; charset=utf-8")
	c.Response.SetBody([]byte(baseResp.GetStatusMessage()))
}
