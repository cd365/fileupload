package fileupload

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

type Storage struct {
	storageDirectory string // 存储目录
	uriAccessPrefix  string // 资源访问前缀
}

type Opts func(s *Storage)

// WithStorageDirectory 磁盘存储目录
func WithStorageDirectory(directory string) Opts {
	return func(s *Storage) { s.storageDirectory = directory }
}

// WithUriAccessPrefix uri资源访问前缀
func WithUriAccessPrefix(prefix string) Opts {
	return func(s *Storage) { s.uriAccessPrefix = prefix }
}

func NewStorage(
	opts ...Opts,
) *Storage {
	s := &Storage{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// FileStorage 文件存储参数
type FileStorage struct {
	StorageDirectory    string // 文件存储目录
	UriAccessPrefix     string // 资源访问前缀
	StorageSubDirectory string // 文件保存子目录
}

// FileStorageResult 文件存储结果
type FileStorageResult struct {
	Uid        int64  `json:"uid,omitempty"`      // 文件唯一id
	Size       int64  `json:"size"`               // 文件大小
	Bucket     string `json:"bucket,omitempty"`   // 文件存储桶
	Category   string `json:"category,omitempty"` // 资源分类
	Name       string `json:"name"`               // 文件名
	Hash       string `json:"hash,omitempty"`     // 文件哈希值(sha256)
	FileExt    string `json:"file_ext"`           // 文件后缀
	PathAbs    string `json:"path_abs,omitempty"` // 文件存储绝对路径
	PathRlt    string `json:"path_rlt,omitempty"` // 文件存储相对路径
	PathUri    string `json:"path_uri"`           // 文件资源访问路径
	OriginName string `json:"origin_name"`        // 原始文件名
}

func (s *Storage) multipartCopy(param *FileStorage, file *multipart.FileHeader) (result *FileStorageResult, err error) {
	result = &FileStorageResult{
		Size:       file.Size,
		OriginName: file.Filename,
	}

	src, err := file.Open()
	if err != nil {
		return
	}
	defer func() { _ = src.Close() }()

	result.Hash, err = s.sha256Reader(src)
	if err != nil {
		return
	}

	// 下次从文件起始处读取文件内容
	if _, err = src.Seek(0, 0); err != nil {
		return
	}

	result.FileExt = path.Ext(file.Filename)
	// filename
	result.Name = result.Hash + result.FileExt

	storageDirectory := s.storageDirectory
	if param.StorageDirectory != "" {
		storageDirectory = param.StorageDirectory
	}
	saveDirectory := storageDirectory

	result.PathUri = result.Name
	if param.StorageSubDirectory != "" {
		storageDirectory = path.Join(storageDirectory, param.StorageSubDirectory)
		result.PathUri = path.Join(param.StorageSubDirectory, result.PathUri)
	}

	result.PathRlt = path.Join(storageDirectory, result.Name)
	if filepath.IsAbs(result.PathRlt) {
		result.PathAbs = result.PathRlt
		result.PathRlt = strings.TrimPrefix(result.PathRlt, saveDirectory)
	} else {
		result.PathAbs, err = filepath.Abs(result.PathRlt)
		if err != nil {
			return
		}
	}

	if _, err = os.Stat(result.PathAbs); err != nil {
		if os.IsNotExist(err) {
			if err = os.MkdirAll(storageDirectory, 0755); err != nil {
				return
			}
		}
	}

	uriAccessPrefix := s.uriAccessPrefix
	if param.UriAccessPrefix != "" {
		uriAccessPrefix = param.UriAccessPrefix
	}
	if uriAccessPrefix != "" {
		result.PathUri = path.Join(uriAccessPrefix, result.PathUri)
	}
	if !strings.HasPrefix(result.PathUri, "/") {
		result.PathUri = "/" + result.PathUri
	}
	if os.PathSeparator != '/' {
		result.PathUri = strings.ReplaceAll(result.PathUri, string(os.PathSeparator), "/")
	}

	if stat, ser := os.Stat(result.PathAbs); ser == nil {
		if stat.Size() == result.Size && !stat.IsDir() {
			if err = os.Remove(result.PathAbs); err != nil {
				return
			}
		}
	}

	dst, err := os.Create(result.PathAbs)
	if err != nil {
		return
	}
	defer func() { _ = dst.Close() }()

	if _, err = io.Copy(dst, src); err != nil {
		return
	}

	return
}

// MultipartCopy 文件拷贝
func (s *Storage) MultipartCopy(param *FileStorage, files ...*multipart.FileHeader) (succeeded []*FileStorageResult, err error) {
	var tmp *FileStorageResult
	length := len(files)
	succeeded = make([]*FileStorageResult, 0, length)
	for i := 0; i < length; i++ {
		if files[i] == nil {
			continue
		}
		tmp, err = s.multipartCopy(param, files[i])
		if err != nil {
			return
		}
		succeeded = append(succeeded, tmp)
	}
	return
}

var regexpImageBase64 = regexp.MustCompile(`^data:\s*image/(\w+);base64,(.*)`)

func (s *Storage) base64Copy(param *FileStorage, content []byte) (result *FileStorageResult, err error) {
	result = &FileStorageResult{}
	matched := regexpImageBase64.FindAllSubmatch(content, -1)
	if len(matched) == 0 || len(matched[0]) < 3 {
		err = fmt.Errorf("illegal image base64 value")
		return
	}
	imageContent, err := base64.StdEncoding.DecodeString(string(matched[0][2]))
	if err != nil {
		return
	}
	result.FileExt = "." + string(matched[0][1])
	result.Hash, err = s.sha256Reader(bytes.NewBuffer(content))
	if err != nil {
		return
	}
	result.Name = result.Hash + result.FileExt

	storageDirectory := s.storageDirectory
	if param.StorageDirectory != "" {
		storageDirectory = param.StorageDirectory
	}
	saveDirectory := storageDirectory

	result.PathUri = result.Name
	if param.StorageSubDirectory != "" {
		storageDirectory = path.Join(storageDirectory, param.StorageSubDirectory)
		result.PathUri = path.Join(param.StorageSubDirectory, result.PathUri)
	}
	result.PathRlt = path.Join(storageDirectory, result.Name)
	if filepath.IsAbs(result.PathRlt) {
		result.PathAbs = result.PathRlt
		result.PathRlt = strings.TrimPrefix(result.PathRlt, saveDirectory)
	} else {
		result.PathAbs, err = filepath.Abs(result.PathRlt)
		if err != nil {
			return
		}
	}

	if _, err = os.Stat(result.PathAbs); err != nil {
		if os.IsNotExist(err) {
			if err = os.MkdirAll(storageDirectory, 0755); err != nil {
				return
			}
		}
	}

	uriAccessPrefix := s.uriAccessPrefix
	if param.UriAccessPrefix != "" {
		uriAccessPrefix = param.UriAccessPrefix
	}
	if uriAccessPrefix != "" {
		result.PathUri = path.Join(uriAccessPrefix, result.PathUri)
	}
	if !strings.HasPrefix(result.PathUri, "/") {
		result.PathUri = "/" + result.PathUri
	}
	if os.PathSeparator != '/' {
		result.PathUri = strings.ReplaceAll(result.PathUri, string(os.PathSeparator), "/")
	}

	if stat, ser := os.Stat(result.PathAbs); ser == nil {
		if !stat.IsDir() {
			if err = os.Remove(result.PathAbs); err != nil {
				return
			}
		}
	}

	fil, err := os.Create(result.PathAbs)
	if err != nil {
		return
	}
	defer func() { _ = fil.Close() }()

	if _, err = io.Copy(fil, bytes.NewBuffer(imageContent)); err != nil {
		return
	}

	return
}

// Base64Copy 图片base64存储
func (s *Storage) Base64Copy(param *FileStorage, files [][]byte) (succeeded []*FileStorageResult, err error) {
	var tmp *FileStorageResult
	length := len(files)
	for i := 0; i < length; i++ {
		if files[i] == nil {
			continue
		}
		if tmp, err = s.base64Copy(param, files[i]); err != nil {
			return
		} else {
			succeeded = append(succeeded, tmp)
		}
	}
	return
}

func (s *Storage) sha256Reader(r io.Reader) (string, error) {
	tmp := sha256.New()
	if _, err := io.Copy(tmp, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(tmp.Sum(nil)), nil
}

// IterateResult 迭代处理存储结果
func (s *Storage) IterateResult(moves []*FileStorageResult, fn func(move *FileStorageResult)) {
	for _, v := range moves {
		fn(v)
	}
}

// MultipartFileName 表单字段名称
type MultipartFileName struct {
	Single   string // 字段名-单文件
	Multiple string // 字段名-多文件
}

// Echo 文件上传echo
func (s *Storage) Echo(c echo.Context, param *FileStorage, name *MultipartFileName) (succeeded []*FileStorageResult, err error) {
	if name == nil {
		return
	}
	// single file
	if name.Single != "" {
		var file *multipart.FileHeader
		file, err = c.FormFile(name.Single)
		if err != nil {
			return
		}
		var tmp *FileStorageResult
		tmp, err = s.multipartCopy(param, file)
		if err != nil {
			return
		}
		succeeded = append(succeeded, tmp)
	}
	// multiple files
	if name.Multiple != "" {
		var form *multipart.Form
		form, err = c.MultipartForm()
		if err != nil {
			return
		}
		defer func() { _ = form.RemoveAll() }()
		var tmp []*FileStorageResult
		tmp, err = s.MultipartCopy(param, form.File[name.Multiple]...)
		if err != nil {
			return
		}
		succeeded = append(succeeded, tmp...)
	}
	return
}

// SubDirectoryDate 子目录附日期
func (s *Storage) SubDirectoryDate(subDirectory string) string {
	now := time.Now()
	return path.Join(subDirectory, fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", now.Month()), fmt.Sprintf("%02d", now.Day()))
}
