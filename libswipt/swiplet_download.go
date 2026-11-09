package libSwipt

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/swipt-automation/swipt/internal/automation"
)

const (
	sourceLimit        = 4 << 20
	archiveBlock       = 64 << 10
	swipletIdleTimeout = 15 * time.Second
)

var swipletClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{Timeout: swipletIdleTimeout}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &idleConn{conn}, nil
	}
	transport.TLSHandshakeTimeout = swipletIdleTimeout
	return &http.Client{Transport: transport}
}()

type idleConn struct {
	net.Conn
}

func (c *idleConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(swipletIdleTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c *idleConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(swipletIdleTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

type SwipletDownload struct {
	URL         string
	EntityTag   string
	Name        string
	ID          string
	Version     string
	VersionCode int64
}

const (
	SwipletErrorNone int32 = iota
	SwipletErrorFailed
	SwipletErrorOutdated
	SwipletErrorIOSOutdated
	SwipletErrorSwipletOutdated
)

type SwipletDownloader struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSwipletDownloader() *SwipletDownloader {
	ctx, cancel := context.WithCancel(context.Background())
	return &SwipletDownloader{ctx: ctx, cancel: cancel}
}

func (d *SwipletDownloader) Cancel() {
	d.cancel()
}

type SwipletDownloadHandler interface {
	OnProgress(received int64, total int64)
}

func (d *SwipletDownloader) Download(link string, destination string, checksum string, handler SwipletDownloadHandler) (*SwipletDownload, error) {
	download, err := d.download(link, destination, checksum, handler)
	if err != nil {
		return nil, E.Cause(err, "download ", link)
	}
	return download, nil
}

func (d *SwipletDownloader) download(link string, destination string, checksum string, handler SwipletDownloadHandler) (*SwipletDownload, error) {
	response, err := d.get(link, http.Header{})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, E.New(response.Status)
	}
	download := &SwipletDownload{URL: response.Request.URL.String(), EntityTag: response.Header.Get("ETag")}
	name := response.Request.URL.Path
	if _, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		name = params["filename"]
	}
	name = path.Base(name)
	download.Name = strings.TrimSuffix(name, path.Ext(name))
	file, err := os.Create(destination + ".download")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash, &progressWriter{handler: handler, total: response.ContentLength}), response.Body)
	if err != nil {
		return nil, err
	}
	if checksum != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), checksum) {
		return nil, E.New("the download doesn't match the checksum")
	}
	return finish(download, file, size, link, destination)
}

type progressWriter struct {
	handler  SwipletDownloadHandler
	received int64
	total    int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.received += int64(len(p))
	w.handler.OnProgress(w.received, w.total)
	return len(p), nil
}

type FetchedManifest struct {
	EntityTag   string
	NotModified bool
	Manifest    *SwipletManifest
}

func (d *SwipletDownloader) FetchManifest(link string, etag string) (*FetchedManifest, error) {
	fetched, err := d.fetchManifest(link, etag)
	if err != nil {
		return nil, E.Cause(err, "fetch ", link)
	}
	return fetched, nil
}

func (d *SwipletDownloader) fetchManifest(link string, etag string) (*FetchedManifest, error) {
	header := http.Header{}
	header.Set("Range", fmt.Sprintf("bytes=0-%d", archiveBlock-1))
	if etag != "" {
		header.Set("If-None-Match", etag)
	}
	response, err := d.get(link, header)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	fetched := &FetchedManifest{EntityTag: response.Header.Get("ETag")}
	switch response.StatusCode {
	case http.StatusNotModified:
		fetched.NotModified = true
		return fetched, nil
	case http.StatusOK, http.StatusPartialContent:
	default:
		return nil, E.New(response.Status)
	}
	head, size, err := readSegment(response)
	if err != nil {
		return nil, err
	}
	archive := &remoteArchive{downloader: d, url: response.Request.URL.String(), size: size, segments: []archiveSegment{head}}
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return nil, E.Cause(err, "open the archive")
	}
	contents, err := automation.ReadArchive(reader)
	if err != nil {
		return nil, err
	}
	fetched.Manifest = newSwipletManifest(contents.Manifest)
	return fetched, nil
}

type remoteArchive struct {
	downloader *SwipletDownloader
	url        string
	size       int64
	segments   []archiveSegment
}

type archiveSegment struct {
	offset int64
	data   []byte
}

func (a *remoteArchive) ReadAt(p []byte, offset int64) (int, error) {
	end := min(offset+int64(len(p)), a.size)
	if offset >= end {
		return 0, io.EOF
	}
	segment, err := a.segment(offset, end)
	if err != nil {
		return 0, err
	}
	n := copy(p, segment.data[offset-segment.offset:end-segment.offset])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (a *remoteArchive) segment(start int64, end int64) (archiveSegment, error) {
	for _, segment := range a.segments {
		if segment.covers(start, end) {
			return segment, nil
		}
	}
	from := max(0, min(start, a.size-archiveBlock))
	to := min(a.size, max(end, from+archiveBlock))
	header := http.Header{}
	header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to-1))
	response, err := a.downloader.get(a.url, header)
	if err != nil {
		return archiveSegment{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return archiveSegment{}, E.New(response.Status)
	}
	segment, _, err := readSegment(response)
	if err != nil {
		return archiveSegment{}, err
	}
	if !segment.covers(start, end) {
		return archiveSegment{}, E.New("the server sent bytes other than ", from, "-", to-1)
	}
	a.segments = append(a.segments, segment)
	return segment, nil
}

func (s archiveSegment) covers(start int64, end int64) bool {
	return s.offset <= start && end <= s.offset+int64(len(s.data))
}

func readSegment(response *http.Response) (archiveSegment, int64, error) {
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return archiveSegment{}, 0, err
	}
	if response.StatusCode == http.StatusOK {
		return archiveSegment{data: data}, int64(len(data)), nil
	}
	var start, end, size int64
	if _, err = fmt.Sscanf(response.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &size); err != nil {
		return archiveSegment{}, 0, E.Cause(err, "read Content-Range ", strconv.Quote(response.Header.Get("Content-Range")))
	}
	return archiveSegment{offset: start, data: data}, size, nil
}

type FetchResult struct {
	EntityTag   string
	NotModified bool
	Data        []byte
}

func (d *SwipletDownloader) Fetch(link string, etag string, asSource bool) (*FetchResult, error) {
	fetched, err := d.fetch(link, etag, asSource)
	if err != nil {
		return nil, E.Cause(err, "fetch ", link)
	}
	return fetched, nil
}

func (d *SwipletDownloader) fetch(link string, etag string, asSource bool) (*FetchResult, error) {
	header := http.Header{}
	if etag != "" {
		header.Set("If-None-Match", etag)
	}
	response, err := d.get(link, header)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	fetched := &FetchResult{EntityTag: response.Header.Get("ETag")}
	switch response.StatusCode {
	case http.StatusNotModified:
		fetched.NotModified = true
		return fetched, nil
	case http.StatusOK:
	default:
		return nil, E.New(response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, sourceLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > sourceLimit {
		return nil, E.New("the file is larger than ", sourceLimit>>20, " MB")
	}
	if asSource {
		var listed source
		if err = json.Unmarshal(data, &listed); err != nil {
			return nil, E.Cause(err, "read the source")
		}
		if listed.Swiplets == nil {
			return nil, E.New("the file is not a source")
		}
	}
	fetched.Data = data
	return fetched, nil
}

func (d *SwipletDownloader) get(link string, header http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(d.ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		return nil, E.New("unsupported URL")
	}
	request.Header = header
	response, err := swipletClient.Do(request)
	if urlError, isURL := errors.AsType[*url.Error](err); isURL {
		return nil, urlError.Err
	}
	return response, err
}

type source struct {
	Swiplets []json.RawMessage `json:"swiplets"`
}

func finish(download *SwipletDownload, archive io.ReaderAt, size int64, link string, destination string) (*SwipletDownload, error) {
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return nil, E.Cause(err, "open the archive")
	}
	contents, err := automation.ReadArchive(reader)
	if err != nil {
		return nil, err
	}
	download.ID, download.Version, download.VersionCode = contents.Manifest.ID, contents.Manifest.Version, contents.Manifest.VersionCode
	file, err := os.Create(destination)
	if err != nil {
		return nil, err
	}
	writer := zip.NewWriter(file)
	err = copyArchive(writer, contents, link)
	if err = E.Errors(err, writer.Close(), file.Close()); err != nil {
		return nil, err
	}
	return download, nil
}

func copyArchive(writer *zip.Writer, contents automation.Archive, link string) error {
	for _, source := range contents.Files {
		if source.Name == automation.ManifestFile && contents.Manifest.UpdateURL == "" {
			manifest := contents.Manifest
			manifest.UpdateURL = link
			data, err := automation.EncodeManifest(manifest)
			if err != nil {
				return err
			}
			output, err := writer.CreateHeader(&zip.FileHeader{Name: source.Name, Method: zip.Deflate, Modified: source.Modified})
			if err != nil {
				return err
			}
			if _, err = output.Write(data); err != nil {
				return err
			}
			continue
		}
		header := source.FileHeader
		header.Name, header.Extra = source.Name, nil
		output, err := writer.CreateRaw(&header)
		if err != nil {
			return err
		}
		input, err := source.OpenRaw()
		if err != nil {
			return err
		}
		if _, err = io.Copy(output, input); err != nil {
			return err
		}
	}
	return nil
}
