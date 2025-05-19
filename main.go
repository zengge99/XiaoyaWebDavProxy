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
	name    string
	size    int64
	modTime time.Time
	isDir   bool
	content []byte // 对于目录为空
}

func NewVirtualFileSystem() *VirtualFileSystem {
	fmt.Println("Creating new VirtualFileSystem")
	return &VirtualFileSystem{
		files: make(map[string]*VirtualFile),
	}
}

// 从文本描述加载文件系统
func (vfs *VirtualFileSystem) LoadFromText(text string) error {
	fmt.Println("Loading file system from text")
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "#")
		if len(parts) != 2 {
			return fmt.Errorf("invalid line format: %s", line)
		}

		path := strings.TrimSpace(parts[0])
		sizeStr := strings.TrimSpace(parts[1])

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
					fmt.Printf("Creating directory: %s\n", dirPath)
					vfs.files[dirPath] = &VirtualFile{
						name:    filepath.Base(dirPath),
						size:    0,
						modTime: time.Now(),
						isDir:   true,
					}
				}
			}
		}

		fmt.Printf("Adding file: %s, size: %d\n", path, size)
		vfs.files[path] = &VirtualFile{
			name:    filepath.Base(path),
			size:    size,
			modTime: time.Now(),
			isDir:   false,
		}
	}

	// 确保根目录存在
	if _, exists := vfs.files["/"]; !exists {
		fmt.Println("Creating root directory")
		vfs.files["/"] = &VirtualFile{
			name:    "",
			size:    0,
			modTime: time.Now(),
			isDir:   true,
		}
	}

	return nil
}

// 以下是实现 webdav.FileSystem 接口的方法，都添加了 context.Context 参数

func (vfs *VirtualFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	fmt.Printf("Mkdir called for: %s\n", name)
	if _, exists := vfs.files[name]; exists {
		fmt.Printf("Directory already exists: %s\n", name)
		return os.ErrExist
	}
	vfs.files[name] = &VirtualFile{
		name:    filepath.Base(name),
		size:    0,
		modTime: time.Now(),
		isDir:   true,
	}
	fmt.Printf("Directory created: %s\n", name)
	return nil
}

func (vfs *VirtualFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	fmt.Printf("OpenFile called for: %s, flags: %d\n", name, flag)
	f, exists := vfs.files[name]
	if !exists {
		if flag&os.O_CREATE != 0 {
			fmt.Printf("Creating new file: %s\n", name)
			f = &VirtualFile{
				name:    filepath.Base(name),
				size:    0,
				modTime: time.Now(),
				isDir:   false,
			}
			vfs.files[name] = f
			return &VirtualFileHandle{file: f}, nil
		}
		fmt.Printf("File not found: %s\n", name)
		return nil, os.ErrNotExist
	}

	if flag&os.O_EXCL != 0 && flag&os.O_CREATE != 0 {
		fmt.Printf("File already exists (O_EXCL): %s\n", name)
		return nil, os.ErrExist
	}

	fmt.Printf("Returning file handle for: %s\n", name)
	return &VirtualFileHandle{file: f}, nil
}

func (vfs *VirtualFileSystem) RemoveAll(ctx context.Context, name string) error {
	fmt.Printf("RemoveAll called for: %s\n", name)
	toDelete := make([]string, 0)
	for path := range vfs.files {
		if path == name || strings.HasPrefix(path, name+"/") {
			toDelete = append(toDelete, path)
		}
	}

	if len(toDelete) == 0 {
		fmt.Printf("No files to delete for: %s\n", name)
		return os.ErrNotExist
	}

	for _, path := range toDelete {
		fmt.Printf("Deleting: %s\n", path)
		delete(vfs.files, path)
	}
	return nil
}

func (vfs *VirtualFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	fmt.Printf("Rename called from: %s to: %s\n", oldName, newName)
	oldFile, exists := vfs.files[oldName]
	if !exists {
		fmt.Printf("Source file not found: %s\n", oldName)
		return os.ErrNotExist
	}

	// 如果是目录，需要重命名所有子文件和目录
	if oldFile.isDir {
		fmt.Printf("Renaming directory and its contents\n")
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
			fmt.Printf("Moving %s to %s\n", oldName, newPath)
			vfs.files[newPath] = file
		}
	} else {
		fmt.Printf("Moving file from %s to %s\n", oldName, newName)
		delete(vfs.files, oldName)
		vfs.files[newName] = oldFile
	}

	return nil
}

func (vfs *VirtualFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	fmt.Printf("Stat called for: %s\n", name)
	f, exists := vfs.files[name]
	if !exists {
		fmt.Printf("File not found: %s\n", name)
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
		fmt.Printf("File already closed: %s\n", vf.file.name)
		return os.ErrClosed
	}
	vf.closed = true
	fmt.Printf("File closed: %s\n", vf.file.name)
	return nil
}

func (vf *VirtualFileHandle) Read(p []byte) (n int, err error) {
	if vf.closed {
		fmt.Printf("Attempt to read closed file: %s\n", vf.file.name)
		return 0, os.ErrClosed
	}
	if vf.file.isDir {
		fmt.Printf("Attempt to read directory as file: %s\n", vf.file.name)
		return 0, os.ErrInvalid
	}
	if vf.offset >= vf.file.size {
		fmt.Printf("Read beyond EOF: %s, offset: %d, size: %d\n", vf.file.name, vf.offset, vf.file.size)
		return 0, io.EOF
	}
	n = copy(p, make([]byte, vf.file.size-vf.offset))
	vf.offset += int64(n)
	fmt.Printf("Read %d bytes from %s, new offset: %d\n", n, vf.file.name, vf.offset)
	return n, nil
}

func (vf *VirtualFileHandle) Seek(offset int64, whence int) (int64, error) {
	if vf.closed {
		fmt.Printf("Attempt to seek closed file: %s\n", vf.file.name)
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
		fmt.Printf("Invalid whence value: %d for file: %s\n", whence, vf.file.name)
		return 0, errors.New("seek: invalid whence")
	}
	if vf.offset < 0 {
		fmt.Printf("Negative seek offset: %d for file: %s\n", vf.offset, vf.file.name)
		return 0, errors.New("seek: negative position")
	}
	fmt.Printf("Seek file: %s to offset: %d\n", vf.file.name, vf.offset)
	return vf.offset, nil
}

func (vf *VirtualFileHandle) Readdir(count int) ([]os.FileInfo, error) {
	if vf.closed {
		fmt.Printf("Attempt to readdir closed file: %s\n", vf.file.name)
		return nil, os.ErrClosed
	}
	if !vf.file.isDir {
		fmt.Printf("Attempt to readdir non-directory: %s\n", vf.file.name)
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

	fmt.Printf("Readdir for %s returned %d items\n", vf.file.name, len(infos))
	return infos, nil
}

func (vf *VirtualFileHandle) Stat() (os.FileInfo, error) {
	if vf.closed {
		fmt.Printf("Attempt to stat closed file: %s\n", vf.file.name)
		return nil, os.ErrClosed
	}
	fmt.Printf("Stat called on file handle: %s\n", vf.file.name)
	return vf.file, nil
}

func (vf *VirtualFileHandle) Write(p []byte) (n int, err error) {
	if vf.closed {
		fmt.Printf("Attempt to write closed file: %s\n", vf.file.name)
		return 0, os.ErrClosed
	}
	if vf.file.isDir {
		fmt.Printf("Attempt to write directory: %s\n", vf.file.name)
		return 0, os.ErrInvalid
	}
	vf.file.size = vf.offset + int64(len(p))
	vf.file.modTime = time.Now()
	fmt.Printf("Wrote %d bytes to %s, new size: %d\n", len(p), vf.file.name, vf.file.size)
	return len(p), nil
}

// VirtualFile 实现 os.FileInfo 接口
func (vf *VirtualFile) Name() string {
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
	fileList := `/a/战狼2.mkv#65342
/a/b/哪吒闹海.mkv#3389
/哪吒闹海.mkv#1024`

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
		fmt.Printf("\n=== New Request ===\n")
		fmt.Printf("Method: %s\n", r.Method)
		fmt.Printf("URL: %s\n", r.URL.Path)
		fmt.Printf("Headers: %v\n", r.Header)

		username, password, ok := r.BasicAuth()
		if !ok {
			fmt.Println("No auth provided")
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		fmt.Printf("Auth attempt - username: %s, password: %s\n", username, password)
		
		if username != "1" || password != "1" {
			fmt.Println("Invalid credentials")
			w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		fmt.Println("Authentication successful")

		switch r.Method {
		case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "MKCOL", "COPY", "MOVE", "PROPFIND", "PROPPATCH", "LOCK", "UNLOCK":
			fmt.Printf("Handling WebDAV method: %s\n", r.Method)
			dav.ServeHTTP(w, r)
		default:
			fmt.Printf("Unsupported method: %s\n", r.Method)
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