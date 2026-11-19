// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

func (s *Server) registerVectorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vector-store/stats", s.vectorStats)
	mux.HandleFunc("GET /api/vector-store/collections/{collection}", s.listVectorDocuments)
	mux.HandleFunc("GET /api/vector-store/collections/{collection}/{doc_id}", s.vectorDocument)
	mux.HandleFunc("DELETE /api/vector-store/collections/{collection}/clear", s.clearVectorCollection)
	mux.HandleFunc("DELETE /api/vector-store/collections/{collection}/{doc_id}", s.deleteVectorDocument)
	mux.HandleFunc("DELETE /api/vector-store/collections/{collection}", s.deleteVectorBatch)
	mux.HandleFunc("POST /api/vector-store/search", s.searchVectors)
}

func (s *Server) vectorStore(w http.ResponseWriter) *vectors.Store {
	if s.deps.Vector == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "向量存储不可用")
	}
	return s.deps.Vector
}

func (s *Server) vectorStats(w http.ResponseWriter, r *http.Request) {
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	stats, err := store.Stats(r.Context())
	if err != nil {
		s.vectorFailure(w, err)
		return
	}
	writeEnvelopeOK(w, map[string]any{
		"available": stats.Available, "total_vectors": stats.SummariesCount,
		"summaries": map[string]any{"available": stats.Available, "total_vectors": stats.SummariesCount},
		"messages":  map[string]any{"available": stats.Available, "total_vectors": stats.MessagesCount},
	}, "")
}

func (s *Server) vectorFailure(w http.ResponseWriter, err error) {
	s.logger.Error("向量存储操作失败", "error", err)
	writeHTTPError(w, http.StatusInternalServerError, "向量存储操作失败")
}

func vectorCollection(w http.ResponseWriter, r *http.Request) string {
	kind := r.PathValue("collection")
	if kind == "summaries" || kind == "messages" {
		return kind
	}
	writeHTTPError(w, http.StatusBadRequest, "不支持的集合名称: "+kind)
	return ""
}

func vectorQueryInt(r *http.Request, key string, def, floor, ceiling int) (int, bool) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return def, true
	}
	i, err := strconv.Atoi(value)
	return i, err == nil && i >= floor && (ceiling == 0 || i <= ceiling)
}

type vectorDocumentPayload struct {
	ID        string         `json:"id"`
	Document  string         `json:"document"`
	Metadata  map[string]any `json:"metadata"`
	Dimension *int           `json:"embedding_dimension,omitempty"`
}

func newVectorDocument(row vectors.StoredRow) vectorDocumentPayload {
	meta := row.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	return vectorDocumentPayload{ID: row.ID, Document: row.Document, Metadata: meta}
}

func (s *Server) listVectorDocuments(w http.ResponseWriter, r *http.Request) {
	kind := vectorCollection(w, r)
	if kind == "" {
		return
	}
	limit, limitOK := vectorQueryInt(r, "limit", 50, 1, 200)
	offset, offsetOK := vectorQueryInt(r, "offset", 0, 0, 0)
	if !limitOK || !offsetOK {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的分页参数")
		return
	}
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	total, err := store.Count(r.Context(), kind)
	if err != nil {
		s.vectorFailure(w, err)
		return
	}
	docs := []vectorDocumentPayload{}
	if offset < total {
		limit = min(limit, total-offset)
		rows, err := store.GetRows(r.Context(), kind, vectors.GetFilter{
			IncludeDocs: true, IncludeMeta: true, Limit: limit, Offset: offset,
		})
		if err != nil {
			s.vectorFailure(w, err)
			return
		}
		for _, row := range rows {
			docs = append(docs, newVectorDocument(row))
		}
	}
	writeEnvelopeOK(w, map[string]any{
		"documents": docs, "total": total, "limit": limit, "offset": offset, "available": true,
	}, "")
}

func (s *Server) vectorDocument(w http.ResponseWriter, r *http.Request) {
	kind := vectorCollection(w, r)
	if kind == "" {
		return
	}
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	row, err := store.GetDocument(r.Context(), kind, r.PathValue("doc_id"))
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && row == nil:
		writeHTTPError(w, http.StatusNotFound, "文档不存在: "+r.PathValue("doc_id"))
	case err != nil:
		s.vectorFailure(w, err)
	default:
		body := newVectorDocument(*row)
		body.Dimension = &row.Dim
		writeEnvelopeOK(w, body, "")
	}
}

func (s *Server) clearVectorCollection(w http.ResponseWriter, r *http.Request) {
	kind := vectorCollection(w, r)
	if kind == "" {
		return
	}
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	count, err := store.ClearCollection(r.Context(), kind)
	if err != nil {
		s.vectorFailure(w, err)
		return
	}
	message := "集合已经是空的"
	if count > 0 {
		message = fmt.Sprintf("已清空集合 %s（%d 条记录）", kind, count)
	}
	writeEnvelopeOK(w, map[string]int{"deleted_count": count}, message)
}

func (s *Server) deleteVectorDocument(w http.ResponseWriter, r *http.Request) {
	kind := vectorCollection(w, r)
	if kind == "" {
		return
	}
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	id := r.PathValue("doc_id")
	channel, msg, isParent := strings.Cut(id, ":")
	n, parseErr := strconv.ParseInt(msg, 10, 64)
	var err error
	switch {
	case kind == "summaries":
		var summaryID int64
		summaryID, err = strconv.ParseInt(id, 10, 64)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "无效的文档 ID: "+id)
			return
		}
		_, err = store.Delete(r.Context(), kind, []string{strconv.FormatInt(summaryID, 10)})
	case isParent && parseErr == nil && n > 0 && channel != "":
		_, err = store.DeleteMessages(r.Context(), channel, []int64{n})
	default:
		_, err = store.Delete(r.Context(), kind, []string{id})
	}
	if err != nil {
		s.vectorFailure(w, err)
		return
	}
	writeEnvelopeOK(w, nil, "已删除文档: "+id)
}

func (s *Server) deleteVectorBatch(w http.ResponseWriter, r *http.Request) {
	kind := vectorCollection(w, r)
	if kind == "" {
		return
	}
	var ids []string
	if err := readJSONBody(r, &ids, 256*1024); err != nil || ids == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的文档 ID 列表")
		return
	}
	if len(ids) > 500 {
		writeHTTPError(w, http.StatusBadRequest, "批量删除最多支持 500 个文档 ID")
		return
	}
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	if _, err := store.Delete(r.Context(), kind, ids); err != nil {
		s.vectorFailure(w, err)
		return
	}
	writeEnvelopeOK(w, map[string]int{"deleted_count": len(ids)}, fmt.Sprintf("已删除 %d 个文档", len(ids)))
}

func (s *Server) searchVectors(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	kind := r.URL.Query().Get("collection")
	top, ok := vectorQueryInt(r, "top_k", 10, 1, 50)
	if strings.TrimSpace(query) == "" || !ok {
		writeHTTPError(w, http.StatusUnprocessableEntity, "搜索文本或结果数量无效")
		return
	}
	if kind != "" && kind != "all" && kind != "summaries" && kind != "messages" {
		writeHTTPError(w, http.StatusBadRequest, "不支持的集合名称: "+kind)
		return
	}
	store := s.vectorStore(w)
	if store == nil {
		return
	}
	if s.deps.Embedding == nil || !s.deps.Embedding.IsAvailable() {
		writeHTTPError(w, http.StatusServiceUnavailable, "Embedding 服务未配置")
		return
	}
	vec, err := s.deps.Embedding.Generate(r.Context(), query)
	if err != nil {
		s.vectorFailure(w, err)
		return
	}
	hits, err := store.Search(r.Context(), vec, vectors.Options{Collection: kind, TopK: top})
	if err != nil {
		s.vectorFailure(w, err)
		return
	}
	results := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		item := map[string]any{
			"summary_id": nil, "message_id": nil,
			"summary_text": hit.Document, "metadata": hit.Metadata, "doc_id": hit.DocID,
			"similarity": hit.Similarity, "distance": 1 - hit.Similarity,
		}
		kindOfHit := "message"
		if id, err := strconv.ParseInt(hit.DocID, 10, 64); err == nil {
			item["summary_id"] = id
			kindOfHit = "summary"
		}
		if pos := strings.LastIndexByte(hit.DocID, ':'); pos >= 0 {
			if id, err := strconv.ParseInt(hit.DocID[pos+1:], 10, 64); err == nil {
				item["message_id"] = id
			}
		}
		if raw := hit.Metadata["message_id"]; raw != nil && item["message_id"] == nil {
			if id, err := strconv.ParseInt(fmt.Sprint(raw), 10, 64); err == nil {
				item["message_id"] = id
			}
		}
		if kind == "all" || kind == "" {
			item["source"] = kindOfHit
		}
		results = append(results, item)
	}
	writeEnvelopeOK(w, map[string]any{"results": results, "total": len(results), "query": query}, "")
}
