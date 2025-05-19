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

// 文件元数据结构
type FileMeta struct {
	Path        string
	Size        int64
	DisplayName string
	Content     []byte
}

// 模拟的文件系统
type TextWebDAVFileSystem struct {
	mu    sync.RWMutex
	Files map[string]*FileMeta
	Auth  map[string]string
	Port  int
}

// 虚拟文件实现
type VirtualFile struct {
	meta  *FileMeta
	pos   int64
	fs    *TextWebDAVFileSystem
	flags int
}

// 虚拟文件信息
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
	fs.Auth["用户名1"] = "密码1"
	fmt.Printf("添加用户: 用户名=用户名1, 密码=密码1\n")

	// 加载模拟数据
	err := fs.LoadFromText(`
# 格式: 路径#大小#displayname
/1.mkv#1024#哪吒2(2025)
/2.pdf#512#项目报告2025
/docs/3.txt#128#重要笔记
`)
	if err != nil {
		fmt.Printf("加载数据错误: %v\n", err)
		return
	}

	// 设置WebDAV处理器
	handler := &webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		PropSystem: &CustomPropSystem{fs: fs},
	}

	// 添加认证中间件
	authHandler := fs.authMiddleware(handler)

	// 启动服务器
	addr := fmt.Sprintf(":%d", fs.Port)
	fmt.Printf("WebDAV 模拟器启动在端口 %d\n", fs.Port)
	fmt.Printf("使用用户名: 用户名1, 密码: 密码1 访问\n")
	fmt.Printf("测试URL: http://localhost:%d\n", fs.Port)

	err = http.ListenAndServe(addr, authHandler)
	if err != nil {
		fmt.Printf("服务器错误: %v\n", err)
	}
}

// 从文本加载模拟数据
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

		fmt.Printf("加载文件: 路径=%s, 大小=%d, 显示名=%s\n", path, size, displayName)
	}

	return nil
}

// 认证中间件
func (fs *TextWebDAVFileSystem) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("收到请求: %s %s\n", r.Method, r.URL.Path)

		username, password, ok := r.BasicAuth()
		if !ok {
			fmt.Printf("请求缺少认证头\n")
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "需要认证", http.StatusUnauthorized)
			return
		}

		if fs.Auth[username] != password {
			fmt.Printf("认证失败: 用户名=%s\n", username)
			http.Error(w, "认证失败", http.StatusUnauthorized)
			return
		}

		fmt.Printf("认证成功: 用户名=%s\n", username)
		next.ServeHTTP(w, r)
	})
}

// 实现webdav.FileSystem接口的所有必需方法

func (fs *TextWebDAVFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	fmt.Printf("OpenFile: %s (flag=%d)\n", name, flag)

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

	fmt.Printf("Stat: %s\n", name)

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

func (fs *TextWebDAVFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	fmt.Printf("Mkdir attempted: %s (read-only)\n", name)
	return os.ErrPermission
}

func (fs *TextWebDAVFileSystem) RemoveAll(ctx context.Context, name string) error {
	fmt.Printf("RemoveAll attempted: %s (read-only)\n", name)
	return os.ErrPermission
}

func (fs *TextWebDAVFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	fmt.Printf("Rename attempted: %s -> %s (read-only)\n", oldName, newName)
	return os.ErrPermission
}

// 虚拟文件方法
func (f *VirtualFile) Close() error {
	fmt.Printf("Close: %s\n", f.meta.Path)
	return nil
}

func (f *VirtualFile) Read(p []byte) (int, error) {
	if f.pos >= int64(len(f.meta.Content)) {
		return 0, io.EOF
	}
	n := copy(p, f.meta.Content[f.pos:])
	f.pos += int64(n)
	fmt.Printf("Read %d bytes from %s (pos=%d)\n", n, f.meta.Path, f.pos)
	return n, nil
}

func (f *VirtualFile) Write(p []byte) (int, error) {
	fmt.Printf("Write attempted on %s (read-only)\n", f.meta.Path)
	return 0, os.ErrPermission
}

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
	fmt.Printf("Seek %s to %d (whence=%d)\n", f.meta.Path, f.pos, whence)
	return f.pos, nil
}

func (f *VirtualFile) Readdir(count int) ([]os.FileInfo, error) {
	fmt.Printf("Readdir attempted on %s (not a directory)\n", f.meta.Path)
	return nil, os.ErrPermission
}

func (f *VirtualFile) Stat() (os.FileInfo, error) {
	return f.fs.Stat(context.Background(), f.meta.Path)
}

// 虚拟文件信息方法
func (fi *VirtualFileInfo) Name() string       { return fi.name }
func (fi *VirtualFileInfo) Size() int64        { return fi.size }
func (fi *VirtualFileInfo) Mode() os.FileMode  { return 0444 }
func (fi *VirtualFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *VirtualFileInfo) IsDir() bool        { return fi.isDir }
func (fi *VirtualFileInfo) Sys() interface{}   { return nil }

// 自定义属性系统
type CustomPropSystem struct {
	fs *TextWebDAVFileSystem
}

func (c *CustomPropSystem) Find(ctx context.Context, name string, propnames []xml.Name) (map[xml.Name]webdav.Property, error) {
	c.fs.mu.RLock()
	defer c.fs.mu.RUnlock()

	fmt.Printf("PropFind: %s - 查找属性: %v\n", name, propnames)

	result := make(map[xml.Name]webdav.Property)
	meta, ok := c.fs.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}

	for _, propname := range propnames {
		if propname.Space == "DAV:" {
			switch propname.Local {
			case "displayname":
				result[propname] = webdav.Property{
					XMLName:  propname,
					InnerXML: []byte(meta.DisplayName),
				}
			case "getcontentlength":
				result[propname] = webdav.Property{
					XMLName:  propname,
					InnerXML: []byte(fmt.Sprintf("%d", meta.Size)),
				}
			case "getlastmodified":
				result[propname] = webdav.Property{
					XMLName:  propname,
					InnerXML: []byte(time.Now().Format(http.TimeFormat)),
				}
			case "resourcetype":
				result[propname] = webdav.Property{
					XMLName:  propname,
					InnerXML: []byte("<D:resourcetype/>"),
				}
			}
		}
	}

	return result, nil
}

func (c *CustomPropSystem) Patch(ctx context.Context, name string, patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	c.fs.mu.Lock()
	defer c.fs.mu.Unlock()

	fmt.Printf("PropPatch: %s - 修改属性: %+v\n", name, patches)

	meta, ok := c.fs.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}

	var propstats []webdav.Propstat
	for _, patch := range patches {
		var stat webdav.Propstat
		for _, prop := range patch.Props {
			if prop.XMLName.Space == "DAV:" && prop.XMLName.Local == "displayname" {
				meta.DisplayName = string(prop.InnerXML)
				stat.Props = append(stat.Props, webdav.Property{
					XMLName:  prop.XMLName,
					InnerXML: prop.InnerXML,
				})
				fmt.Printf("修改displayname: %s -> %s\n", name, meta.DisplayName)
			} else {
				stat.Status = http.StatusForbidden
			}
		}
		if len(stat.Props) > 0 {
			stat.Status = http.StatusOK
		}
		propstats = append(propstats, stat)
	}

	return propstats, nil
}

func (c *CustomPropSystem) RemoveAll(ctx context.Context, name string) error {
	fmt.Printf("RemoveAll attempted on %s (not allowed)\n", name)
	return os.ErrPermission
}

func (c *CustomPropSystem) Copy(ctx context.Context, srcName, dstName string, recursive bool) error {
	fmt.Printf("Copy attempted from %s to %s (not allowed)\n", srcName, dstName)
	return os.ErrPermission
}
