package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

// VirtualFileSystem 实现 webdav.FileSystem 接口
type VirtualFileSystem struct {
	files map[string]*VirtualFile
}

type VirtualFile struct {
	name        string    // 实际文件名
	displayName string    // 显示名称
	size        int64
	modTime     time.Time
	isDir       bool
	content     []byte
}

func NewVirtualFileSystem() *VirtualFileSystem {
	return &VirtualFileSystem{
		files: make(map[string]*VirtualFile),
	}
}

// 从文本描述加载文件系统
func (vfs *VirtualFileSystem) LoadFromText(text string) error {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "#")
		if len(parts) < 2 {
			return fmt.Errorf("invalid line format: %s", line)
		}

		path := strings.TrimSpace(parts[0])
		sizeStr := strings.TrimSpace(parts[1])
		
		// 默认显示名称为文件名
		displayName := filepath.Base(path)
		// 如果有第三个部分，则作为显示名称
		if len(parts) >= 3 {
			displayName = strings.TrimSpace(parts[2])
		}

		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid size in line: %s", line)
		}

		// 确保所有父目录都存在
		dir := filepath.Dir(path)
		if dir != "." && dir != "/" {
			parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
			current := ""
			for _, part := range parts {
				current = filepath.Join(current, part)
				dirPath := "/" + current
				if _, exists := vfs.files[dirPath]; !exists {
					vfs.files[dirPath] = &VirtualFile{
						name:        filepath.Base(dirPath),
						displayName: filepath.Base(dirPath),
						size:        0,
						modTime:     time.Now(),
						isDir:       true,
					}
				}
			}
		}

		vfs.files[path] = &VirtualFile{
			name:        filepath.Base(path),
			displayName: displayName,
			size:        size,
			modTime:     time.Now(),
			isDir:       false,
		}
	}

	// 确保根目录存在
	if _, exists := vfs.files["/"]; !exists {
		vfs.files["/"] = &VirtualFile{
			name:        "",
			displayName: "",
			size:        0,
			modTime:     time.Now(),
			isDir:       true,
		}
	}

	return nil
}

// 以下是实现 webdav.FileSystem 接口的方法
func (vfs *VirtualFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	if _, exists := vfs.files[name]; exists {
		return os.ErrExist
	}
	vfs.files[name] = &VirtualFile{
		name:        filepath.Base(name),
		displayName: filepath.Base(name),
		size:        0,
		modTime:     time.Now(),
		isDir:       true,
	}
	return nil
}

func (vfs *VirtualFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	f, exists := vfs.files[name]
	if !exists {
		if flag&os.O_CREATE != 0 {
			f = &VirtualFile{
				name:        filepath.Base(name),
				displayName: filepath.Base(name),
				size:        0,
				modTime:     time.Now(),
				isDir:       false,
			}
			vfs.files[name] = f
			return &VirtualFileHandle{file: f}, nil
		}
		return nil, os.ErrNotExist
	}

	if flag&os.O_EXCL != 0 && flag&os.O_CREATE != 0 {
		return nil, os.ErrExist
	}

	return &VirtualFileHandle{file: f}, nil
}

func (vfs *VirtualFileSystem) RemoveAll(ctx context.Context, name string) error {
	toDelete := make([]string, 0)
	for path := range vfs.files {
		if path == name || strings.HasPrefix(path, name+"/") {
			toDelete = append(toDelete, path)
		}
	}

	for _, path := range toDelete {
		delete(vfs.files, path)
	}
	return nil
}

func (vfs *VirtualFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldFile, exists := vfs.files[oldName]
	if !exists {
		return os.ErrNotExist
	}

	if oldFile.isDir {
		children := make(map[string]*VirtualFile)
		for path, file := range vfs.files {
			if path == oldName || strings.HasPrefix(path, oldName+"/") {
				newPath := newName + strings.TrimPrefix(path, oldName)
				children[newPath] = file
			}
		}

		for path := range vfs.files {
			if path == oldName || strings.HasPrefix(path, oldName+"/") {
				delete(vfs.files, path)
			}
		}

		for newPath, file := range children {
			vfs.files[newPath] = file
		}
	} else {
		delete(vfs.files, oldName)
		vfs.files[newName] = oldFile
	}

	return nil
}

func (vfs *VirtualFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	f, exists := vfs.files[name]
	if !exists {
		return nil, os.ErrNotExist
	}
	return f, nil
}

// VirtualFileHandle 实现 webdav.File 接口
type VirtualFileHandle struct {
	file    *VirtualFile
	offset  int64
	closed  bool
}

func (vf *VirtualFileHandle) Close() error {
	if vf.closed {
		return os.ErrClosed
	}
	vf.closed = true
	return nil
}

func (vf *VirtualFileHandle) Read(p []byte) (n int, err error) {
	if vf.closed {
		return 0, os.ErrClosed
	}
	if vf.file.isDir {
		return 0, os.ErrInvalid
	}
	if vf.offset >= vf.file.size {
		return 0, io.EOF
	}
	n = copy(p, make([]byte, vf.file.size-vf.offset))
	vf.offset += int64(n)
	return n, nil
}

func (vf *VirtualFileHandle) Seek(offset int64, whence int) (int64, error) {
	if vf.closed {
		return 0, os.ErrClosed
	}
	switch whence {
	case io.SeekStart:
		vf.offset = offset
	case io.SeekCurrent:
		vf.offset += offset
	case io.SeekEnd:
		vf.offset = vf.file.size + offset
	default:
		return 0, errors.New("seek: invalid whence")
	}
	if vf.offset < 0 {
		return 0, errors.New("seek: negative position")
	}
	return vf.offset, nil
}

func (vf *VirtualFileHandle) Readdir(count int) ([]os.FileInfo, error) {
	if vf.closed {
		return nil, os.ErrClosed
	}
	if !vf.file.isDir {
		return nil, os.ErrInvalid
	}

	var infos []os.FileInfo
	for path, file := range vfs.files {
		dir := filepath.Dir(path)
		if dir == strings.TrimSuffix(vf.file.name, "/") || 
           (dir == "." && vf.file.name == "") || 
           (dir == "/" && vf.file.name == "") {
			infos = append(infos, file)
		}
	}

	if count > 0 && len(infos) > count {
		infos = infos[:count]
	}

	return infos, nil
}

func (vf *VirtualFileHandle) Stat() (os.FileInfo, error) {
	if vf.closed {
		return nil, os.ErrClosed
	}
	return vf.file, nil
}

func (vf *VirtualFileHandle) Write(p []byte) (n int, err error) {
	if vf.closed {
		return 0, os.ErrClosed
	}
	if vf.file.isDir {
		return 0, os.ErrInvalid
	}
	vf.file.size = vf.offset + int64(len(p))
	vf.file.modTime = time.Now()
	return len(p), nil
}

// VirtualFile 实现 os.FileInfo 接口
func (vf *VirtualFile) Name() string {
	// 返回显示名称而不是实际文件名
	if vf.displayName != "" {
		return vf.displayName
	}
	return vf.name
}

func (vf *VirtualFile) Size() int64 {
	return vf.size
}

func (vf *VirtualFile) Mode() os.FileMode {
	if vf.isDir {
		return os.ModeDir | 0755
	}
	return 0644
}

func (vf *VirtualFile) ModTime() time.Time {
	return vf.modTime
}

func (vf *VirtualFile) IsDir() bool {
	return vf.isDir
}

func (vf *VirtualFile) Sys() interface{} {
	return nil
}

var vfs = NewVirtualFileSystem()

func main() {
	// 示例文件列表
	fileList := `/a/战狼2.mkv#65342#战狼2(2017)
/a/b/哪吒闹海.mkv#3389#哪吒闹海(1979)
/哪吒闹海.mkv#1024#哪吒2(2025)`

	// 加载虚拟文件系统
	err := vfs.LoadFromText(fileList)
	if err != nil {
		fmt.Printf("Error loading file system: %v\n", err)
		return
	}

	// 设置WebDAV处理器
	dav := &webdav.Handler{
		FileSystem: vfs,
		LockSystem: webdav.NewMemLS(),
	}

	// 设置HTTP路由
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "1" || password != "1" {
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "MKCOL", "COPY", "MOVE", "PROPFIND", "PROPPATCH", "LOCK", "UNLOCK":
			dav.ServeHTTP(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// 启动服务器
	port := "39124"
	fmt.Printf("WebDAV server running on port %s...\n", port)
	fmt.Println("Use username: 1, password: 1 to access")
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}