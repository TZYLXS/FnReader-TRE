package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/cacert.pem
var embeddedCACert []byte

type busJob struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Age     int64           `json:"age"`
}

type pendingResp struct {
	Jobs   []busJob `json:"jobs"`
	WaitMs int      `json:"waitMs"`
}

type apiClient struct {
	base    string
	token   string
	devID   string
	devName string
	devArch string
	ver     string
	ctl     *http.Client
	dl      *http.Client
}

func newHTTPClient() *http.Client {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if len(embeddedCACert) > 0 {
		roots.AppendCertsFromPEM(embeddedCACert)
	}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{RootCAs: roots},
		ResponseHeaderTimeout: 40 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: tr}
}

func newAPIClient(cfg *Config, devID, devName, version string) *apiClient {
	return &apiClient{
		base:    cfg.Server,
		token:   cfg.Token,
		devID:   devID,
		devName: devName,
		devArch: "openwrt",
		ver:     version,
		ctl:     newHTTPClient(),
		dl:      newHTTPClient(),
	}
}

func (c *apiClient) absURL(p string) string {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return c.base + p
}

func (c *apiClient) newRequest(ctx context.Context, method, p string, q url.Values, body io.Reader) (*http.Request, error) {
	u := c.absURL(p)
	if len(q) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u = u + sep + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("X-Api-Token", c.token)
	}
	req.Header.Set("User-Agent", "fnparse/"+c.ver+" (openwrt)")
	return req, nil
}

func readErrBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	s := strings.TrimSpace(string(b))
	if s == "" {
		return resp.Status
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		if e.Code != "" {
			return e.Code + ": " + e.Error
		}
		return e.Error
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, s)
}

func (c *apiClient) getJSON(ctx context.Context, p string, q url.Values, out interface{}) error {
	req, err := c.newRequest(ctx, http.MethodGet, p, q, nil)
	if err != nil {
		return err
	}
	resp, err := c.ctl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(readErrBody(resp))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

func (c *apiClient) postJSON(ctx context.Context, p string, q url.Values, in, out interface{}, extra map[string]string) error {
	body, err := jsonNoEscape(in)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPost, p, q, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.ctl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(readErrBody(resp))
	}
	if out == nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&struct{}{})
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func jsonNoEscape(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (c *apiClient) pending(ctx context.Context, wait int) ([]busJob, error) {
	if c.token == "" {
		return nil, errors.New("未配置访问令牌（Token）")
	}
	q := url.Values{
		"id":    {c.devID},
		"name":  {c.devName},
		"model": {c.devArch},
		"ver":   {c.ver},
		"wait":  {strconv.Itoa(wait)},
		"limit": {"5"},
	}
	var out pendingResp
	if err := c.getJSON(ctx, "/api/m/remote/pending", q, &out); err != nil {
		return nil, err
	}
	return out.Jobs, nil
}

func (c *apiClient) postResult(ctx context.Context, id string, ok bool, data interface{}, errCode string) error {
	body := map[string]interface{}{
		"id":       id,
		"ok":       ok,
		"deviceId": c.devID,
	}
	if ok {
		body["data"] = data
	} else {
		body["error"] = errCode
	}
	var out struct {
		OK       bool `json:"ok"`
		Accepted bool `json:"accepted"`
	}
	if err := c.postJSON(ctx, "/api/m/remote/result", nil, body, &out, nil); err != nil {
		return err
	}
	if !out.Accepted && !out.OK {
		return errors.New("任务已被服务端回收（可能已兜底）")
	}
	return nil
}

// Download 整本下载，支持 Range 续传；expectSize>0 时校验大小。
func (c *apiClient) Download(ctx context.Context, fileURL, dest string, expectSize int64) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	var have int64
	if st, err := os.Stat(dest); err == nil {
		have = st.Size()
	}
	if expectSize > 0 && have == expectSize {
		return nil
	}
	req, err := c.newRequest(ctx, http.MethodGet, fileURL, nil, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := c.dl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		have = 0
	case http.StatusPartialContent:
		if have == 0 {
			return errors.New("服务端返回 206 但本地没有分段文件")
		}
	case http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(dest)
		if expectSize > 0 && have >= expectSize {
			return nil
		}
		return fmt.Errorf("服务端拒绝续传（已下载 %d 字节）", have)
	case http.StatusNotFound:
		return errors.New("原文件不存在（404）")
	default:
		return errors.New(readErrBody(resp))
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if have == 0 {
		if err := f.Truncate(0); err != nil {
			f.Close()
			return err
		}
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		return err
	}
	total := have + n
	if expectSize > 0 && total != expectSize {
		return fmt.Errorf("下载大小不符：期望 %d，实际 %d", expectSize, total)
	}
	return nil
}

type partResp struct {
	OK       bool  `json:"ok"`
	Chapters int   `json:"chapters"`
	Total    int   `json:"total"`
	Received []int `json:"received"`
	Complete bool  `json:"complete"`
}

func (c *apiClient) UploadPart(ctx context.Context, sha1 string, seq, total int, body []byte) (*partResp, error) {
	q := url.Values{"sha1": {sha1}, "seq": {strconv.Itoa(seq)}, "total": {strconv.Itoa(total)}}
	payload := body
	extra := map[string]string{}
	if len(body) > 64<<10 {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(body); err == nil && zw.Close() == nil && buf.Len() < len(body) {
			payload = buf.Bytes()
			extra["Content-Encoding"] = "gzip"
		}
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/m/ebook/part", q, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.ctl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(readErrBody(resp))
	}
	var out partResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type partsState struct {
	SHA1     string `json:"sha1"`
	Total    int    `json:"total"`
	Received []int  `json:"received"`
	Complete bool   `json:"complete"`
	Status   string `json:"status"`
	Parts    []int  `json:"parts"`
}

func (c *apiClient) PartsReceived(ctx context.Context, sha1 string) (*partsState, error) {
	var out partsState
	err := c.getJSON(ctx, "/api/m/ebook/parts", url.Values{"sha1": {sha1}}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Received) == 0 && len(out.Parts) > 0 {
		out.Received = out.Parts
	}
	return &out, nil
}

func (c *apiClient) UploadChapter(ctx context.Context, sha1 string, index int, body []byte) error {
	q := url.Values{"sha1": {sha1}, "index": {strconv.Itoa(index)}}
	var out struct {
		OK bool `json:"ok"`
	}
	return c.postJSON(ctx, "/api/m/ebook/chapter", q, json.RawMessage(body), &out, nil)
}

func (c *apiClient) Info(ctx context.Context) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.getJSON(ctx, "/api/m/info", nil, &out)
	return out, err
}

func (c *apiClient) Bus(ctx context.Context) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.getJSON(ctx, "/api/bus", nil, &out)
	return out, err
}

// deriveDeviceID 用 MAC 或 machine-id 生成稳定设备 id；都拿不到就落盘一个随机值。
func deriveDeviceID(cacheRoot string) string {
	var seed string
	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		seed = strings.TrimSpace(string(b))
	}
	if seed == "" {
		if ifs, err := net.Interfaces(); err == nil {
			for _, itf := range ifs {
				if itf.Flags&net.FlagLoopback != 0 || len(itf.HardwareAddr) == 0 {
					continue
				}
				seed = itf.HardwareAddr.String()
				break
			}
		}
	}
	if seed != "" {
		h := sha1.Sum([]byte("fnparse:" + seed))
		return "owrt-" + hex.EncodeToString(h[:])[:10]
	}
	if cacheRoot != "" {
		p := filepath.Join(cacheRoot, "device.id")
		if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return strings.TrimSpace(string(b))
		}
		id := "owrt-" + randomHex(6)
		_ = os.WriteFile(p, []byte(id), 0o644)
		return id
	}
	return "owrt-" + randomHex(6)
}

func randomHex(n int) string {
	b := make([]byte, n)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = byte(now >> (uint(i%7) * 8))
		now = now*6364136223846793005 + 1442695040888963407
	}
	return hex.EncodeToString(b)
}
