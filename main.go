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

// FileMeta 文件元数据结构
type FileMeta struct {
	Path        string
	Size        int64
	DisplayName string
	Content     []byte
}

// TextWebDAVFileSystem 模拟的文件系统
type TextWebDAVFileSystem struct {
	mu    sync.RWMutex
	Files map[string]*FileMeta
	Auth  map[string]string
	Port  int
}

// VirtualFile 虚拟文件实现
type VirtualFile struct {
	meta  *FileMeta
	pos   int64
	fs    *TextWebDAVFileSystem
	flags int
}

// VirtualFileInfo 虚拟文件信息
type VirtualFileInfo struct {
	name    string
	size    int64
	path    string
	isDir   bool
	modTime time.Time
}

func main() {
	// 创建文件系统实例
	fs := &TextWebDAVFileSystem{
		Files: make(map[string]*FileMeta),
		Auth:  make(map[string]string),
		Port:  39124,
	}

	// 添加示例用户
	fs.Auth["1"] = "1"
	fmt.Printf("[系统] 添加用户: 用户名=用户名1, 密码=密码1\n")

	// 加载模拟数据
	err := fs.LoadFromText(`
# 格式: 路径#大小#displayname
/1.mkv#1024#哪吒2(2025)
/2.pdf#512#项目报告2025
/docs/3.txt#128#重要笔记
`)
	if err != nil {
		fmt.Printf("[系统] 加载数据错误: %v\n", err)
		return
	}

	// 设置WebDAV处理器
	handler := &webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}

	// 添加认证中间件
	authHandler := fs.authMiddleware(handler)

	// 启动服务器
	addr := fmt.Sprintf(":%d", fs.Port)
	fmt.Printf("[系统] WebDAV 模拟器启动在端口 %d\n", fs.Port)
	fmt.Printf("[系统] 使用用户名: 用户名1, 密码: 密码1 访问\n")
	fmt.Printf("[系统] 测试URL: http://localhost:%d\n", fs.Port)

	err = http.ListenAndServe(addr, authHandler)
	if err != nil {
		fmt.Printf("[系统] 服务器错误: %v\n", err)
	}
}

// LoadFromText 从文本加载模拟数据
func (fs *TextWebDAVFileSystem) LoadFromText(text string) error {
	scanner := bufio.NewScanner(strings.NewReader(text))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "#")
		if len(parts) < 3 {
			return fmt.Errorf("第%d行格式错误: 需要 path#size#displayname", lineNum)
		}

		path := strings.TrimSpace(parts[0])
		size, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return fmt.Errorf("第%d行大小格式错误: %v", lineNum, err)
		}

		displayName := strings.TrimSpace(parts[2])
		if path == "" || displayName == "" {
			return fmt.Errorf("第%d行路径或显示名不能为空", lineNum)
		}

		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}

		content := []byte(fmt.Sprintf("这是文件 %s 的模拟内容。大小: %d 字节", path, size))

		fs.mu.Lock()
		fs.Files[path] = &FileMeta{
			Path:        path,
			Size:        size,
			DisplayName: displayName,
			Content:     content,
		}
		fs.mu.Unlock()

		fmt.Printf("[系统] 加载文件: 路径=%s, 大小=%d, 显示名=%s\n", path, size, displayName)
	}

	return nil
}

// authMiddleware 认证中间件
func (fs *TextWebDAVFileSystem) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[请求] %s %s\n", r.Method, r.URL.Path)

		username, password, ok := r.BasicAuth()
		if !ok {
			fmt.Printf("[认证] 请求缺少认证头\n")
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "需要认证", http.StatusUnauthorized)
			return
		}

		if fs.Auth[username] != password {
			fmt.Printf("[认证] 失败: 用户名=%s\n", username)
			http.Error(w, "认证失败", http.StatusUnauthorized)
			return
		}

		fmt.Printf("[认证] 成功: 用户名=%s\n", username)
		next.ServeHTTP(w, r)
	})
}

// OpenFile 实现 webdav.FileSystem 接口
func (fs *TextWebDAVFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	fmt.Printf("[操作] OpenFile: %s (flag=%d)\n", name, flag)

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

// Stat 实现 webdav.FileSystem 接口
func (fs *TextWebDAVFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	fmt.Printf("[操作] Stat: %s\n", name)

	meta, ok := fs.Files[name]
	if !ok {
		// 检查是否是目录
		for path := range fs.Files {
			if strings.HasPrefix(path, name+"/") {
				return &VirtualFileInfo{
					name:    filepath.Base(name),
					size:    0,
					path:    name,
					isDir:   true,
					modTime: time.Now(),
				}, nil
			}
		}
		return nil, os.ErrNotExist
	}

	return &VirtualFileInfo{
		name:    meta.DisplayName,
		size:    meta.Size,
		path:    meta.Path,
		isDir:   false,
		modTime: time.Now(),
	}, nil
}

// Mkdir 实现 webdav.FileSystem 接口
func (fs *TextWebDAVFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	fmt.Printf("[操作] Mkdir attempted: %s (read-only)\n", name)
	return os.ErrPermission
}

// RemoveAll 实现 webdav.FileSystem 接口
func (fs *TextWebDAVFileSystem) RemoveAll(ctx context.Context, name string) error {
	fmt.Printf("[操作] RemoveAll attempted: %s (read-only)\n", name)
	return os.ErrPermission
}

// Rename 实现 webdav.FileSystem 接口
func (fs *TextWebDAVFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	fmt.Printf("[操作] Rename attempted: %s -> %s (read-only)\n", oldName, newName)
	return os.ErrPermission
}

// Close 实现文件关闭
func (f *VirtualFile) Close() error {
	fmt.Printf("[文件] Close: %s\n", f.meta.Path)
	return nil
}

// Read 实现文件读取
func (f *VirtualFile) Read(p []byte) (int, error) {
	if f.pos >= int64(len(f.meta.Content)) {
		return 0, io.EOF
	}
	n := copy(p, f.meta.Content[f.pos:])
	f.pos += int64(n)
	fmt.Printf("[文件] Read %d bytes from %s (pos=%d)\n", n, f.meta.Path, f.pos)
	return n, nil
}

// Write 实现文件写入
func (f *VirtualFile) Write(p []byte) (int, error) {
	fmt.Printf("[文件] Write attempted on %s (read-only)\n", f.meta.Path)
	return 0, os.ErrPermission
}

// Seek 实现文件定位
func (f *VirtualFile) Seek(offset int64, whence int) (int64, error) {
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
	fmt.Printf("[文件] Seek %s to %d (whence=%d)\n", f.meta.Path, f.pos, whence)
	return f.pos, nil
}

// Readdir 实现目录读取
func (f *VirtualFile) Readdir(count int) ([]os.FileInfo, error) {
	fmt.Printf("[文件] Readdir attempted on %s (not a directory)\n", f.meta.Path)
	return nil, os.ErrPermission
}

// Stat 获取文件信息
func (f *VirtualFile) Stat() (os.FileInfo, error) {
	return f.fs.Stat(context.Background(), f.meta.Path)
}

// Name 获取文件名
func (fi *VirtualFileInfo) Name() string { return fi.name }

// Size 获取文件大小
func (fi *VirtualFileInfo) Size() int64 { return fi.size }

// Mode 获取文件模式
func (fi *VirtualFileInfo) Mode() os.FileMode {
	if fi.isDir {
		return 0755
	}
	return 0444
}

// ModTime 获取修改时间
func (fi *VirtualFileInfo) ModTime() time.Time { return fi.modTime }

// IsDir 判断是否是目录
func (fi *VirtualFileInfo) IsDir() bool { return fi.isDir }

// Sys 获取底层数据
func (fi *VirtualFileInfo) Sys() interface{} { return nil }
