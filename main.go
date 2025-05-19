package main

import (
	"bufio"
	"context"
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
	IsDir       bool // 添加IsDir字段
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

	// 设置用户名和密码都为1
	fs.Auth["1"] = "1"
	fmt.Printf("WebDAV 模拟器已启动\n")
	fmt.Printf("用户名: 1\n")
	fmt.Printf("密码: 1\n")

	err := fs.LoadFromText(`
/1.mkv#1024#电影文件
/2.pdf#512#文档资料
/docs/3.txt#128#重要笔记
`)
	if err != nil {
		fmt.Printf("加载数据错误: %v\n", err)
		return
	}

	handler := &webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}

	authHandler := fs.authMiddleware(handler)

	addr := fmt.Sprintf(":%d", fs.Port)
	fmt.Printf("服务器运行在端口 %d\n", fs.Port)
	fmt.Printf("访问地址: http://localhost:%d\n", fs.Port)

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
		}
		fs.mu.Unlock()

		// 自动创建父目录
		dir := filepath.Dir(path)
		if dir != "/" {
			fs.mu.Lock()
			if _, ok := fs.Files[dir]; !ok {
				fs.Files[dir] = &FileMeta{
					Path:        dir,
					DisplayName: filepath.Base(dir),
					IsDir:       true,
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
		modTime: time.Now(),
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
				modTime: time.Now(),
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
func (fi *VirtualFileInfo) Mode() os.FileMode  { return 0444 }
func (fi *VirtualFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *VirtualFileInfo) IsDir() bool        { return fi.isDir }
func (fi *VirtualFileInfo) Sys() interface{}   { return nil }
