package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type Server struct {
	store          *Store
	maxUpload      int64
	chunkThreshold int64
	chunkSize      int64
	index          []byte
	app            []byte
	css            []byte
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /app.js", s.handleAppJS)
	mux.HandleFunc("GET /style.css", s.handleCSS)

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/state", s.handleState)

	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("PUT /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/upload/init", s.handleUploadInit)
	mux.HandleFunc("PUT /api/upload/chunk", s.handleUploadChunk)
	mux.HandleFunc("POST /api/upload/complete", s.handleUploadComplete)
	mux.HandleFunc("DELETE /api/upload", s.handleUploadAbort)
	mux.HandleFunc("GET /api/download", s.handleDownload)
	mux.HandleFunc("DELETE /api/file", s.handleDeleteFile)

	mux.HandleFunc("GET /api/texts", s.handleListTexts)
	mux.HandleFunc("POST /api/texts", s.handleCreateText)
	mux.HandleFunc("GET /api/texts/{id}", s.handleGetText)
	mux.HandleFunc("PUT /api/texts/{id}", s.handlePutText)
	mux.HandleFunc("PATCH /api/texts/{id}", s.handlePatchText)
	mux.HandleFunc("DELETE /api/texts/{id}", s.handleDeleteText)

	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
	_, _ = w.Write(s.index)
}

func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(s.app)
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(s.css)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	meta := s.store.FileInfo()
	var file *FileMeta
	if !meta.Updated.IsZero() {
		file = &meta
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":            file,
		"texts":           s.store.ListTexts(),
		"max_upload":      s.maxUpload,
		"max_text":        MaxTextBytes,
		"chunk_threshold": s.chunkThreshold,
		"chunk_size":      s.chunkSize,
		"version":         version,
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.handleRawUpload(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+(8<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "无效的表单上传")
		return
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isTooLarge(err) {
				writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("文件超过上限 %s", formatSize(s.maxUpload)))
			} else {
				writeErr(w, http.StatusBadRequest, "读取上传内容失败")
			}
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		meta, err := s.store.SaveFile(part.FileName(), part, s.maxUpload)
		_ = part.Close()
		if err != nil {
			s.failUpload(w, err)
			return
		}
		writeJSON(w, http.StatusOK, meta)
		return
	}

	writeErr(w, http.StatusBadRequest, "表单里没有 file 字段")
}

func (s *Server) handleRawUpload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.Header.Get("X-Filename")
	}
	if name == "" {
		name = "upload.bin"
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	meta, err := s.store.SaveFile(name, r.Body, s.maxUpload)
	if err != nil {
		s.failUpload(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleUploadInit(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if payload.Size <= 0 {
		writeErr(w, http.StatusBadRequest, "文件大小无效")
		return
	}
	if payload.Size > s.maxUpload {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("文件超过上限 %s", formatSize(s.maxUpload)))
		return
	}

	id, err := s.store.NewChunkSession(payload.Name, payload.Size)
	if err != nil {
		log.Printf("创建分片上传失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "创建上传失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"chunk_size": s.chunkSize,
		"received":   0,
	})
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if id == "" || err != nil || offset < 0 {
		writeErr(w, http.StatusBadRequest, "分片参数错误")
		return
	}
	if r.ContentLength > s.chunkSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "分片过大")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.chunkSize)
	received, err := s.store.AppendChunk(id, offset, r.Body, r.ContentLength)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "上传会话不存在")
		case errors.Is(err, ErrBadOffset):
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    "分片顺序错误",
				"received": received,
			})
		case errors.Is(err, ErrTooLarge) || isTooLarge(err):
			writeErr(w, http.StatusRequestEntityTooLarge, "分片超出文件大小或上限")
		case errors.Is(err, ErrBadChunk):
			writeErr(w, http.StatusBadRequest, "分片内容不完整")
		default:
			log.Printf("分片上传失败: %v", err)
			writeErr(w, http.StatusInternalServerError, "分片上传失败")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": received})
}

func (s *Server) handleUploadComplete(w http.ResponseWriter, r *http.Request) {
	meta, err := s.store.CompleteChunkSession(r.URL.Query().Get("id"))
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "上传会话不存在")
		case errors.Is(err, ErrIncomplete):
			writeErr(w, http.StatusBadRequest, "分片尚未传完")
		default:
			log.Printf("合并分片失败: %v", err)
			writeErr(w, http.StatusInternalServerError, "合并分片失败")
		}
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleUploadAbort(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AbortChunkSession(r.URL.Query().Get("id")); err != nil && !errors.Is(err, ErrNotFound) {
		log.Printf("放弃分片上传失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "放弃上传失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) failUpload(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTooLarge) || isTooLarge(err) {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("文件超过上限 %s", formatSize(s.maxUpload)))
		return
	}
	log.Printf("上传失败: %v", err)
	writeErr(w, http.StatusInternalServerError, "上传失败")
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	f, meta, err := s.store.OpenFile()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "还没有上传文件")
			return
		}
		log.Printf("打开文件失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "读取文件失败")
		return
	}
	defer f.Close()

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", disposition)
	http.ServeContent(w, r, meta.Name, meta.Updated, f)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteFile(); err != nil {
		log.Printf("删除文件失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListTexts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"texts": s.store.ListTexts()})
}

func (s *Server) handleCreateText(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Title string `json:"title"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if b, err := io.ReadAll(r.Body); err != nil {
		if isTooLarge(err) {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求过大")
			return
		}
		writeErr(w, http.StatusBadRequest, "读取请求失败")
		return
	} else if len(b) > 0 {
		if err := json.Unmarshal(b, &payload); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
	}

	meta, err := s.store.CreateText(payload.Title)
	if err != nil {
		s.failText(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) handleGetText(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	meta, content, err := s.store.GetText(id)
	if err != nil {
		s.failText(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      meta.ID,
		"title":   meta.Title,
		"content": content,
		"updated": meta.Updated,
	})
}

func (s *Server) handlePutText(w http.ResponseWriter, r *http.Request) {
	s.putText(w, r, false)
}

func (s *Server) handlePatchText(w http.ResponseWriter, r *http.Request) {
	s.putText(w, r, true)
}

func (s *Server) putText(w http.ResponseWriter, r *http.Request, patch bool) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxTextBytes+(64<<10))
	b, err := io.ReadAll(r.Body)
	if err != nil {
		if isTooLarge(err) {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("文字超过上限 %s", formatSize(MaxTextBytes)))
			return
		}
		writeErr(w, http.StatusBadRequest, "读取请求失败")
		return
	}

	var content *string
	var title *string
	isJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json")

	if isJSON {
		var payload struct {
			Content *string         `json:"content"`
			Title   json.RawMessage `json:"title"`
		}
		if err := json.Unmarshal(b, &payload); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		content = payload.Content
		if len(payload.Title) > 0 && string(payload.Title) != "null" {
			var t string
			if err := json.Unmarshal(payload.Title, &t); err != nil {
				writeErr(w, http.StatusBadRequest, "title 必须是字符串")
				return
			}
			title = &t
		}
	} else {
		c := string(b)
		content = &c
	}

	if title != nil {
		if _, err := s.store.RenameText(id, *title); err != nil {
			s.failText(w, err)
			return
		}
	}
	if content != nil {
		if _, err := s.store.SaveText(id, *content); err != nil {
			s.failText(w, err)
			return
		}
	}

	meta, value, err := s.store.GetText(id)
	if err != nil {
		s.failText(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      meta.ID,
		"title":   meta.Title,
		"content": value,
		"updated": meta.Updated,
	})
}

func (s *Server) handleDeleteText(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteText(id); err != nil {
		s.failText(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) failText(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "文字窗口不存在")
	case errors.Is(err, ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("文字超过上限 %s", formatSize(MaxTextBytes)))
	case errors.Is(err, ErrLastText):
		writeErr(w, http.StatusBadRequest, "至少要保留一个文字窗口")
	case errors.Is(err, ErrTooManyTexts):
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("最多只能有 %d 个文字窗口", MaxTexts))
	default:
		log.Printf("文字操作失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
	}
}

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusNotFound, "文字窗口不存在")
		return 0, false
	}
	return id, true
}

func isTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "request body too large")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "编码失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
