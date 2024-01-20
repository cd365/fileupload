package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cd365/fileupload"
	"github.com/labstack/echo/v4"
)

func main() {

	e := echo.New()

	e.HideBanner = true

	storageDirectory := "/var/files/uploads"
	uriAccessPrefix := "/resource/static"
	s := fileupload.NewStorage(
		fileupload.WithStorageDirectory(storageDirectory),
		fileupload.WithUriAccessPrefix(uriAccessPrefix),
	)

	// 文件存储参数
	fs := func(c echo.Context) *fileupload.FileStorage {
		subDirectory := "default"
		sd := c.Request().Header.Get("SubDirectory")
		sd = strings.TrimSpace(sd)
		if sd != "" {
			subDirectory = sd
		}
		tmp := &fileupload.FileStorage{
			// 子目录取名 考虑 业务模块, 程序版本, 项目名称
			StorageSubDirectory: s.SubDirectoryDate(path.Join("project1", subDirectory)),
		}
		return tmp
	}
	// 表单文件字段名称
	mfn := func() *fileupload.MultipartFileName {
		return &fileupload.MultipartFileName{
			Single:   "file",
			Multiple: "files",
		}
	}

	// 静态资源注册
	e.Static(uriAccessPrefix, storageDirectory)

	v1 := e.Group(
		"/v1",
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				// todo 中间件鉴权
				// if false {
				// 	return c.String(500, "非法请求")
				// }
				return next(c)
			}
		},
	)

	// 表单文件上传
	v1.POST("/upload", func(c echo.Context) error {
		result, err := s.Echo(c, fs(c), mfn())
		if err != nil {
			return c.String(500, err.Error())
		}
		s.IterateResult(result, func(move *fileupload.FileStorageResult) {
			move.PathAbs = ""
			move.PathRlt = ""
		})
		return c.JSON(200, result)
	})

	// base64文件上传
	v1.POST("/upload/base64", func(c echo.Context) error {
		s64 := make([]string, 0)
		if err := c.Bind(&s64); err != nil {
			return c.String(500, err.Error())
		}
		length := len(s64)
		b64 := make([][]byte, length)
		for i := 0; i < length; i++ {
			b64[i] = []byte(s64[i])
		}
		result, err := s.Base64Copy(fs(c), b64)
		if err != nil {
			return c.String(500, err.Error())
		}
		s.IterateResult(result, func(move *fileupload.FileStorageResult) {
			move.PathAbs = ""
			move.PathRlt = ""
		})
		return c.JSON(200, result)
	})

	wg := &sync.WaitGroup{}
	defer wg.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdown := make(chan error, 1)
	defer close(shutdown)

	exit := &sync.Once{}
	quit := func(err error) {
		if err != nil {
			exit.Do(func() { shutdown <- err })
		}
	}

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		if err := e.Start(":7878"); err != nil {
			quit(err)
			fmt.Println(err.Error())
		}
	}(ctx)

	notify := make(chan os.Signal, 1)
	defer close(notify)
	signal.Notify(
		notify,
		syscall.SIGUSR1, syscall.SIGUSR2,
		syscall.SIGHUP, syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT,
	)

	select {
	case msg := <-notify:
		fmt.Println(msg.String())
	case msg := <-shutdown:
		fmt.Println(msg.Error())
	}

	{
		c1, c2 := context.WithTimeout(ctx, time.Second*30)
		defer c2()
		if err := e.Shutdown(c1); err != nil {
			fmt.Println("shutdown http server:", err.Error())
		}
	}

}
