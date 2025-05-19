package main

import (
	"context"
	"encoding/xml"
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

type VirtualFileSystem struct {
	files map[string]*VirtualFile
}

type VirtualFile struct {
	name        string
	displayName string  // 自定义显示名称
	size        int64
	modTime     time.Time
	isDir       bool
	content     []byte
	properties  map[xml.Name]webdav.Property
}

func NewVirtualFileSystem() *VirtualFileSystem {
	fmt.Println("[INIT] Creating new VirtualFileSystem")
	return &VirtualFileSystem{
		files: make(map[string]*VirtualFile),
	}
}

// 关键修改1：增强文件加载逻辑
func (vfs *VirtualFileSystem) LoadFromText(text string) error {
	fmt.Println("[LOAD] Loading file system from text")
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 解析格式：path#size#displayname
		parts := strings.Split(line, "#")
		if len(parts) < 2 {
			return fmt.Errorf("invalid line format: %s", line)
		}

		path := strings.TrimSpace(parts[0])
		sizeStr := strings.TrimSpace(parts[1])
		displayName := ""
		
		if len(parts) >= 3 {
			displayName = strings.TrimSpace(parts[2])
			fmt.Printf("[LOAD] Found custom displayname: %s\n", displayName)
		}

		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid size in line: %s", line)
		}

		// 创建父目录
		dir := filepath.Dir(path)
		if dir != "." && dir != "/" {
			parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
			current := ""
			for _, part := range parts {
				current = filepath.Join(current, part)
				dirPath := "/" + current
				if _, exists := vfs.files[dirPath]; !exists {
					fmt.Printf("[MKDIR] Creating directory: %s\n", dirPath)
					vfs.files[dirPath] = &VirtualFile{
						name:        filepath.Base(dirPath),
						displayName: filepath.Base(dirPath),
						size:        0,
						modTime:     time.Now(),
						isDir:       true,
						properties:  make(map[xml.Name]webdav.Property),
					}
					// 强制设置目录的displayname属性
					vfs.setDisplayName(dirPath, filepath.Base(dirPath))
				}
			}
		}

		if displayName == "" {
			displayName = filepath.Base(path)
			fmt.Printf("[LOAD] Using default displayname: %s\n", displayName)
		}

		fmt.Printf("[ADD] File: %s, Size: %d, DisplayName: %s\n", path, size, displayName)
		vfs.files[path] = &VirtualFile{
			name:        filepath.Base(path),
			displayName: displayName,
			size:        size,
			modTime:     time.Now(),
			isDir:       false,
			properties:  make(map[xml.Name]webdav.Property),
		}
		// 关键修改：确保属性正确设置
		vfs.setDisplayName(path, displayName)
	}

	// 确保根目录存在
	if _, exists := vfs.files["/"]; !exists {
		fmt.Println("[ROOT] Creating root directory")
		vfs.files["/"] = &VirtualFile{
			name:        "",
			displayName: "Root",
			size:        0,
			modTime:     time.Now(),
			isDir:       true,
			properties:  make(map[xml.Name]webdav.Property),
		}
		vfs.setDisplayName("/", "Root")
	}

	return nil
}

// 关键修改2：专用方法设置displayname
func (vfs *VirtualFileSystem) setDisplayName(path, name string) {
	if file, exists := vfs.files[path]; exists {
		file.displayName = name
		file.properties[xml.Name{Space: "DAV:", Local: "displayname"}] = webdav.Property{
			XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
			InnerXML: []byte(name),
		}
		fmt.Printf("[PROP] Set displayname for %s to: %s\n", path, name)
	}
}

// 关键修改3：重写DeadProps方法
func (vf *VirtualFile) DeadProps() (map[xml.Name]webdav.Property, error) {
	fmt.Printf("[PROP] Getting properties for: %s (displayname=%s)\n", vf.name, vf.displayName)
	
	// 创建新的属性集合，确保包含displayname
	props := make(map[xml.Name]webdav.Property)
	
	// 1. 首先放入displayname（确保优先）
	props[xml.Name{Space: "DAV:", Local: "displayname"}] = webdav.Property{
		XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
		InnerXML: []byte(vf.displayName),
	}
	
	// 2. 合并其他属性
	for k, v := range vf.properties {
		if k.Local != "displayname" { // 避免重复
			props[k] = v
		}
	}
	
	return props, nil
}

func (vf *VirtualFile) Patch(patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	fmt.Printf("[PATCH] Modifying properties for: %s\n", vf.name)
	for _, patch := range patches {
		for _, prop := range patch.Props {
			// 特殊处理displayname
			if prop.XMLName.Local == "displayname" {
				newName := string(prop.InnerXML)
				fmt.Printf("[PATCH] Updating displayname from '%s' to '%s'\n", 
					vf.displayName, newName)
				vf.displayName = newName
			}
			vf.properties[prop.XMLName] = prop
		}
	}
	return []webdav.Propstat{{
		Status: http.StatusOK,
		Props:  []webdav.Property{},
	}}, nil
}

// 实现webdav.FileSystem接口（其他方法保持不变）
func (vfs *VirtualFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	fmt.Printf("[MKDIR] Creating directory: %s\n", name)
	if _, exists := vfs.files[name]; exists {
		return os.ErrExist
	}
	vfs.files[name] = &VirtualFile{
		name:        filepath.Base(name),
		displayName: filepath.Base(name),
		size:        0,
		modTime:     time.Now(),
		isDir:       true,
		properties:  make(map[xml.Name]webdav.Property),
	}
	vfs.setDisplayName(name, filepath.Base(name))
	return nil
}

func (vfs *VirtualFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
    fmt.Printf("OpenFile called for: %s, flags: %d\n", name, flag)
    f, exists := vfs.files[name]
    if !exists {
        if flag&os.O_CREATE != 0 {
            fmt.Printf("Creating new file: %s\n", name)
            f = &VirtualFile{
                name:        filepath.Base(name),
                displayName: filepath.Base(name),
                size:        0,
                modTime:     time.Now(),
                isDir:       false,
                properties:  make(map[xml.Name]webdav.Property),
            }
            // 设置新文件的 displayname 属性
            f.properties[xml.Name{Space: "DAV:", Local: "displayname"}] = webdav.Property{
                XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
                InnerXML: []byte(filepath.Base(name)),
            }
            vfs.files[name] = f
            
            // 打印所有属性
            fmt.Println("File properties:")
            for key, prop := range f.properties {
                fmt.Printf("  %s/%s: %s\n", key.Space, key.Local, string(prop.InnerXML))
            }
            
            return &VirtualFileHandle{file: f}, nil
        }
        fmt.Printf("File not found: %s\n", name)
        return nil, os.ErrNotExist
    }

    // 打印现有文件的所有属性
    fmt.Println("Existing file properties:")
    for key, prop := range f.properties {
        fmt.Printf("  %s/%s: %s\n", key.Space, key.Local, string(prop.InnerXML))
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

// 在 VirtualFileSystem 结构体定义后添加以下方法

func (vfs *VirtualFileSystem) PropFind(ctx context.Context, name string, propnames []xml.Name) ([]webdav.Propstat, error) {
    fmt.Printf("[PROPFIND] Request for: %s, props: %v\n", name, propnames)

    file, exists := vfs.files[name]
    if !exists {
        fmt.Printf("[PROPFIND] File not found: %s\n", name)
        return nil, os.ErrNotExist
    }

    // 获取文件的所有属性
    allProps, err := file.DeadProps()
    if err != nil {
        fmt.Printf("[PROPFIND] Error getting properties for %s: %v\n", name, err)
        return nil, err
    }

    // 如果没有指定属性名，返回所有属性
    if len(propnames) == 0 {
        fmt.Printf("[PROPFIND] Returning all properties for %s\n", name)
        var props []webdav.Property
        for _, prop := range allProps {
            props = append(props, prop)
        }
        return []webdav.Propstat{{
            Status: http.StatusOK,
            Props:  props,
        }}, nil
    }

    // 处理请求的特定属性
    var foundProps []webdav.Property
    var notFoundProps []xml.Name

    for _, pn := range propnames {
        // 处理标准 DAV 属性
        if pn.Space == "DAV:" {
            switch pn.Local {
            case "displayname":
                foundProps = append(foundProps, webdav.Property{
                    XMLName:  pn,
                    InnerXML: []byte(file.displayName),
                })
                continue
            case "getcontentlength":
                if !file.isDir {
                    foundProps = append(foundProps, webdav.Property{
                        XMLName:  pn,
                        InnerXML: []byte(strconv.FormatInt(file.size, 10)),
                    })
                }
                continue
            case "getlastmodified":
                foundProps = append(foundProps, webdav.Property{
                    XMLName:  pn,
                    InnerXML: []byte(file.modTime.Format(time.RFC1123)),
                })
                continue
            case "resourcetype":
                var resType string
                if file.isDir {
                    resType = "<D:collection/>"
                } else {
                    resType = ""
                }
                foundProps = append(foundProps, webdav.Property{
                    XMLName:  pn,
                    InnerXML: []byte(resType),
                })
                continue
            case "getcontenttype":
                if !file.isDir {
                    contentType := "application/octet-stream"
                    ext := strings.ToLower(filepath.Ext(file.name))
                    switch ext {
                    case ".txt":
                        contentType = "text/plain"
                    case ".html", ".htm":
                        contentType = "text/html"
                    case ".jpg", ".jpeg":
                        contentType = "image/jpeg"
                    case ".png":
                        contentType = "image/png"
                    case ".mkv":
                        contentType = "video/x-matroska"
                    }
                    foundProps = append(foundProps, webdav.Property{
                        XMLName:  pn,
                        InnerXML: []byte(contentType),
                    })
                }
                continue
            }
        }

        // 检查自定义属性
        if prop, ok := allProps[pn]; ok {
            foundProps = append(foundProps, prop)
        } else {
            notFoundProps = append(notFoundProps, pn)
        }
    }

    // 构建响应
    var propstats []webdav.Propstat

    if len(foundProps) > 0 {
        propstats = append(propstats, webdav.Propstat{
            Status: http.StatusOK,
            Props:  foundProps,
        })
    }

    if len(notFoundProps) > 0 {
        var notFound []webdav.Property
        for _, pn := range notFoundProps {
            notFound = append(notFound, webdav.Property{XMLName: pn})
        }
        propstats = append(propstats, webdav.Propstat{
            Status: http.StatusNotFound,
            Props:  notFound,
        })
    }

    fmt.Printf("[PROPFIND] Response for %s: %+v\n", name, propstats)
    return propstats, nil
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
	// 返回文件名，不是 displayname
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
	// 示例文件列表，支持 displayname
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