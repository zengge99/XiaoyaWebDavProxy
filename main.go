package main

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

type FileMeta struct {
	Path        string
	Size        int64
	DisplayName string
	Content     []byte
	IsDir       bool
	ModTime     time.Time
}

type TextWebDAVFileSystem struct {
	mu    sync.RWMutex
	Files map[string]*FileMeta
	Auth  map[string]string
	Port  int
}

type VirtualFile struct {
	meta  *FileMeta
	pos   int64
	fs    *TextWebDAVFileSystem
	flags int
}

type VirtualFileInfo struct {
	name    string
	size    int64
	path    string
	isDir   bool
	modTime time.Time
}

func main() {
	fs := &TextWebDAVFileSystem{
		Files: make(map[string]*FileMeta),
		Auth:  make(map[string]string),
		Port:  39124,
	}

	fs.Auth["1"] = "1"
	fmt.Printf("WebDAV 模拟器已启动\n用户名: 1\n密码: 1\n")

	err := fs.LoadFromText(`
/dav/b/玫瑰的故事(2025)_1.mkv#1024#玫瑰的故事(2025)_1.mkv
/dav/b/玫瑰的故事(2025)_2.mkv#1024#玫瑰的故事(2025)_2.mkv
/dav/a/哪吒2(2025)_1.mkv#1024#哪吒2(2025)_1.mkv
`)
	if err != nil {
		fmt.Printf("加载数据错误: %v\n", err)
		return
	}

	handler := &webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}

	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			fs.HandlePropfind(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})

	authHandler := fs.authMiddleware(wrappedHandler)

	addr := fmt.Sprintf(":%d", fs.Port)
	fmt.Printf("服务器运行在端口 %d\n访问地址: http://localhost:%d\n", fs.Port, fs.Port)

	err = http.ListenAndServe(addr, authHandler)
	if err != nil {
		fmt.Printf("服务器错误: %v\n", err)
	}
}

func (fs *TextWebDAVFileSystem) LoadFromText(text string) error {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "#")
		if len(parts) < 3 {
			return fmt.Errorf("格式错误: 需要 path#size#displayname")
		}

		path := strings.TrimSpace(parts[0])
		size, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return fmt.Errorf("大小格式错误: %v", err)
		}

		displayName := strings.TrimSpace(parts[2])
		if path == "" || displayName == "" {
			return fmt.Errorf("路径或显示名不能为空")
		}

		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}

		content := []byte(fmt.Sprintf("模拟文件内容: %s", path))

		fs.mu.Lock()
		fs.Files[path] = &FileMeta{
			Path:        path,
			Size:        size,
			DisplayName: displayName,
			Content:     content,
			IsDir:       false,
			ModTime:     time.Now(),
		}
		fs.mu.Unlock()

		dir := filepath.Dir(path)
		if dir != "/" {
			fs.mu.Lock()
			if _, ok := fs.Files[dir]; !ok {
				fs.Files[dir] = &FileMeta{
					Path:        dir,
					DisplayName: filepath.Base(dir),
					IsDir:       true,
					ModTime:     time.Now(),
				}
			}
			fs.mu.Unlock()
		}

		fmt.Printf("加载文件: %s (%d bytes)\n", path, size)
	}

	return nil
}

func (fs *TextWebDAVFileSystem) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "需要认证", http.StatusUnauthorized)
			return
		}

		if fs.Auth[username] != password {
			http.Error(w, "认证失败", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (fs *TextWebDAVFileSystem) HandlePropfind(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "" {
		path = "/"
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	_, ok := fs.Files[path]
	if !ok && path != "/" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	type Prop struct {
		XMLName        xml.Name `xml:"D:prop"`
		Displayname     *string `xml:"D:displayname,omitempty"`
		Getcontenttype  *string `xml:"D:getcontenttype,omitempty"`
		Getcontentlength *int64  `xml:"D:getcontentlength,omitempty"`
		Getlastmodified *string `xml:"D:getlastmodified,omitempty"`
		Resourcetype    *struct {
			Collection *struct{} `xml:"D:collection,omitempty"`
		} `xml:"D:resourcetype,omitempty"`
	}

	type Propstat struct {
		Prop    Prop   `xml:"D:prop"`
		Status  string `xml:"D:status"`
	}

	type Response struct {
		Href     string   `xml:"D:href"`
		Propstat Propstat `xml:"D:propstat"`
	}

	responses := []Response{}

	if path == "/" || (ok && fs.Files[path].IsDir) {
		displayName := "/"
		modTime := time.Now()
		if path != "/" {
			displayName = fs.Files[path].DisplayName
			modTime = fs.Files[path].ModTime
		}

		responses = append(responses, Response{
			Href: path,
			Propstat: Propstat{
				Status: "HTTP/1.1 200 OK",
				Prop: Prop{
					Displayname:     &displayName,
					Getlastmodified: strPtr(modTime.UTC().Format(http.TimeFormat)),
					Resourcetype: &struct {
						Collection *struct{} `xml:"D:collection,omitempty"`
					}{
						Collection: &struct{}{},
					},
				},
			},
		})

		for filePath, meta := range fs.Files {
			if filepath.Dir(filePath) == path && filePath != path {
				contentType := "application/octet-stream"
				if strings.HasSuffix(filePath, ".txt") {
					contentType = "text/plain"
				} else if strings.HasSuffix(filePath, ".pdf") {
					contentType = "application/pdf"
				} else if strings.HasSuffix(filePath, ".mkv") {
					contentType = "video/x-matroska"
				}

				var resourcetype *struct {
					Collection *struct{} `xml:"D:collection,omitempty"`
				}
				if meta.IsDir {
					resourcetype = &struct {
						Collection *struct{} `xml:"D:collection,omitempty"`
					}{
						Collection: &struct{}{},
					}
				}

				responses = append(responses, Response{
					Href: filePath,
					Propstat: Propstat{
						Status: "HTTP/1.1 200 OK",
						Prop: Prop{
							Displayname:     &meta.DisplayName,
							Getcontenttype:  &contentType,
							Getcontentlength: &meta.Size,
							Getlastmodified: strPtr(meta.ModTime.UTC().Format(http.TimeFormat)),
							Resourcetype:    resourcetype,
						},
					},
				})
			}
		}
	} else {
		meta := fs.Files[path]
		contentType := "application/octet-stream"
		if strings.HasSuffix(path, ".txt") {
			contentType = "text/plain"
		} else if strings.HasSuffix(path, ".pdf") {
			contentType = "application/pdf"
		} else if strings.HasSuffix(path, ".mkv") {
			contentType = "video/x-matroska"
		}

		responses = append(responses, Response{
			Href: path,
			Propstat: Propstat{
				Status: "HTTP/1.1 200 OK",
				Prop: Prop{
					Displayname:     &meta.DisplayName,
					Getcontenttype:  &contentType,
					Getcontentlength: &meta.Size,
					Getlastmodified: strPtr(meta.ModTime.UTC().Format(http.TimeFormat)),
				},
			},
		})
	}

	multistatus := struct {
		XMLName    xml.Name   `xml:"D:multistatus"`
		XmlnsD     string     `xml:"xmlns:D,attr"`
		Responses  []Response `xml:"D:response"`
	}{
		XmlnsD:    "DAV:",
		Responses: responses,
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	xml.NewEncoder(w).Encode(multistatus)
}

func strPtr(s string) *string {
	return &s
}

func (fs *TextWebDAVFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if name == "/" {
		return &VirtualFile{
			meta: &FileMeta{
				Path:        "/",
				DisplayName: "Root",
				Content:     []byte{},
				IsDir:       true,
				ModTime:     time.Now(),
			},
			fs: fs,
		}, nil
	}

	meta, ok := fs.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}

	return &VirtualFile{
		meta:  meta,
		pos:   0,
		fs:    fs,
		flags: flag,
	}, nil
}

func (fs *TextWebDAVFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if name == "/" {
		return &VirtualFileInfo{
			name:    "/",
			size:    0,
			path:    "/",
			isDir:   true,
			modTime: time.Now(),
		}, nil
	}

	meta, ok := fs.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}

	return &VirtualFileInfo{
		name:    meta.DisplayName,
		size:    meta.Size,
		path:    meta.Path,
		isDir:   meta.IsDir,
		modTime: meta.ModTime,
	}, nil
}

func (fs *TextWebDAVFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return os.ErrPermission
}

func (fs *TextWebDAVFileSystem) RemoveAll(ctx context.Context, name string) error {
	return os.ErrPermission
}

func (fs *TextWebDAVFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	return os.ErrPermission
}

func (f *VirtualFile) Close() error {
	return nil
}

func (f *VirtualFile) Read(p []byte) (int, error) {
	if f.meta.IsDir {
		return 0, io.EOF
	}
	if f.pos >= int64(len(f.meta.Content)) {
		return 0, io.EOF
	}
	n := copy(p, f.meta.Content[f.pos:])
	f.pos += int64(n)
	return n, nil
}

func (f *VirtualFile) Write(p []byte) (int, error) {
	return 0, os.ErrPermission
}

func (f *VirtualFile) Seek(offset int64, whence int) (int64, error) {
	if f.meta.IsDir {
		return 0, nil
	}
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = f.pos + offset
	case io.SeekEnd:
		newPos = int64(len(f.meta.Content)) + offset
	default:
		return 0, fmt.Errorf("invalid whence")
	}

	if newPos < 0 {
		return 0, fmt.Errorf("negative position")
	}

	f.pos = newPos
	return f.pos, nil
}

func (f *VirtualFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.meta.IsDir {
		return nil, os.ErrInvalid
	}

	var children []os.FileInfo
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	for path, meta := range f.fs.Files {
		if filepath.Dir(path) == f.meta.Path && path != f.meta.Path {
			children = append(children, &VirtualFileInfo{
				name:    meta.DisplayName,
				size:    meta.Size,
				path:    meta.Path,
				isDir:   meta.IsDir,
				modTime: meta.ModTime,
			})
		}
	}

	return children, nil
}

func (f *VirtualFile) Stat() (os.FileInfo, error) {
	return f.fs.Stat(context.Background(), f.meta.Path)
}

func (fi *VirtualFileInfo) Name() string       { return fi.name }
func (fi *VirtualFileInfo) Size() int64        { return fi.size }
func (fi *VirtualFileInfo) Mode() os.FileMode { 
	if fi.isDir {
		return 0755 
	}
	return 0444 
}
func (fi *VirtualFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *VirtualFileInfo) IsDir() bool        { return fi.isDir }
func (fi *VirtualFileInfo) Sys() interface{}   { return nil }
