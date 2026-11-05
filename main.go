package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "1.0.0"

//go:embed web
var webFS embed.FS

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "轻传盒 %s — 64MB 小内存 VPS 用的文件与文字传输站\n\n", version)
		fmt.Fprintf(flag.CommandLine.Output(), "用法: clipbox [参数]\n\n")
		flag.PrintDefaults()
	}

	var (
		bind          = flag.String("bind", "0.0.0.0", "监听地址")
		port          = flag.Int("port", 28080, "监听端口")
		dir           = flag.String("dir", "./data", "数据目录")
		maxSizeText   = flag.String("max-size", "2G", "单个上传文件上限，支持 B/K/M/G")
		chunkLimit    = flag.String("chunk-threshold", "90M", "超过该大小改用分片上传，支持 B/K/M/G")
		chunkSizeText = flag.String("chunk-size", "8M", "分片大小，支持 B/K/M/G")
		showVersion   = flag.Bool("version", false, "打印版本号后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	maxUpload, err := parseSize(*maxSizeText)
	if err != nil {
		log.Fatalf("--max-size 无效: %v", err)
	}
	chunkThreshold, err := parseSize(*chunkLimit)
	if err != nil {
		log.Fatalf("--chunk-threshold 无效: %v", err)
	}
	chunkSize, err := parseSize(*chunkSizeText)
	if err != nil {
		log.Fatalf("--chunk-size 无效: %v", err)
	}
	if chunkThreshold > maxUpload {
		chunkThreshold = maxUpload
	}
	if chunkSize > maxUpload {
		chunkSize = maxUpload
	}

	store, err := NewStore(*dir)
	if err != nil {
		log.Fatalf("打开数据目录失败: %v", err)
	}

	index, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		log.Fatalf("读取内嵌页面失败: %v", err)
	}
	app, err := fs.ReadFile(webFS, "web/app.js")
	if err != nil {
		log.Fatalf("读取内嵌脚本失败: %v", err)
	}
	css, err := fs.ReadFile(webFS, "web/style.css")
	if err != nil {
		log.Fatalf("读取内嵌样式失败: %v", err)
	}

	srv := &Server{
		store:          store,
		maxUpload:      maxUpload,
		chunkThreshold: chunkThreshold,
		chunkSize:      chunkSize,
		index:          index,
		app:            app,
		css:            css,
	}

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *bind, *port),
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("轻传盒 %s 已启动: http://%s:%d  数据目录=%s  单文件上限=%s  分片阈值=%s 分片大小=%s",
			version, displayHost(*bind), *port, *dir, formatSize(maxUpload),
			formatSize(chunkThreshold), formatSize(chunkSize))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("正在退出…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func displayHost(bind string) string {
	if bind == "0.0.0.0" || bind == "::" || bind == "" {
		return "0.0.0.0"
	}
	return bind
}

func parseSize(raw string) (int64, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return 0, errors.New("不能为空")
	}
	mult := int64(1)
	for _, suf := range []struct {
		name string
		mult int64
	}{
		{"GB", 1 << 30}, {"G", 1 << 30},
		{"MB", 1 << 20}, {"M", 1 << 20},
		{"KB", 1 << 10}, {"K", 1 << 10},
		{"B", 1},
	} {
		if strings.HasSuffix(s, suf.name) {
			mult = suf.mult
			s = strings.TrimSpace(strings.TrimSuffix(s, suf.name))
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("无法解析 %q", raw)
	}
	if n <= 0 {
		return 0, errors.New("必须大于 0")
	}
	if n > math.MaxInt64/mult {
		return 0, errors.New("数值过大")
	}
	return n * mult, nil
}

func formatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
