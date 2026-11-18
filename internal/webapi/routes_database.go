// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
)

func (s *Server) registerDatabaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tables", s.withTableManager(s.listTables))
	mux.HandleFunc("GET /api/tables/{table}", s.withTableManager(s.getTableSchema))
	mux.HandleFunc("GET /api/tables/{table}/rows", s.withTableManager(s.getTableRows))
	mux.HandleFunc("POST /api/tables/{table}/rows", s.withTableManager(s.insertTableRow))
	mux.HandleFunc("PUT /api/tables/{table}/rows/{pk}", s.withTableManager(s.updateTableRow))
	mux.HandleFunc("DELETE /api/tables/{table}/rows/{pk}", s.withTableManager(s.deleteTableRow))
}

func (s *Server) withTableManager(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.DB == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "数据库管理器不可用")
			return
		}
		table := r.PathValue("table")
		if table != "" && !s.deps.DB.TableExists(table) {
			writeHTTPError(w, http.StatusNotFound, "表不存在或不允许访问")
			return
		}
		handler(w, r)
	}
}

func (s *Server) replyDatabaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrTableNotAllowed), errors.Is(err, db.ErrRowNotFound):
		writeHTTPError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, db.ErrInvalidColumn), errors.Is(err, db.ErrNoPrimaryKey):
		writeHTTPError(w, http.StatusBadRequest, err.Error())
	default:
		s.logger.Error("数据库管理操作失败", "error", err)
		writeHTTPError(w, http.StatusInternalServerError, "数据库操作失败")
	}
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.deps.DB.ListTablesWithCounts(r.Context())
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	if tables == nil {
		tables = []db.TableSummary{}
	}
	writeEnvelopeOK(w, tables, "")
}

func (s *Server) getTableSchema(w http.ResponseWriter, r *http.Request) {
	table := r.PathValue("table")
	columns, pk, err := s.deps.DB.GetTableSchema(r.Context(), table)
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	var primary any
	if pk != "" {
		primary = pk
	}
	writeEnvelopeOK(w, map[string]any{"table_name": table, "columns": columns, "primary_key": primary}, "")
}

func (s *Server) getTableRows(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, math.MaxInt/100)
	size := q.number("page_size", 20, 1, 100)
	column := q.text("search_column", "", 0)
	keyword := q.text("search_keyword", "", 100)
	orderBy := q.text("order_by", "", 0)
	order := q.enum("order_dir", "ASC", "ASC", "DESC")
	if !q.valid(w) {
		return
	}
	rows, err := s.deps.DB.QueryTableRowsFiltered(
		r.Context(), r.PathValue("table"), page, size, column, keyword, orderBy, order,
	)
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	writeEnvelopeOK(w, rows, "")
}

func tableRowBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var body struct {
		Data map[string]any `json:"data"`
	}
	if decoder.Decode(&body) != nil || len(body.Data) == 0 || decoder.Decode(new(json.RawMessage)) != io.EOF {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的数据行")
		return nil, false
	}
	for field, value := range body.Data {
		if number, ok := value.(json.Number); ok {
			if n, err := number.Int64(); err == nil {
				body.Data[field] = n
			} else {
				body.Data[field] = number.String()
			}
		}
	}
	return body.Data, true
}

func (s *Server) finishTableWrite(w http.ResponseWriter, r *http.Request, action, target, message string, result any, columns map[string]any, err error, start time.Time) {
	params := ""
	if len(columns) > 0 {
		body, _ := json.Marshal(map[string][]string{"columns": slices.Sorted(maps.Keys(columns))})
		params = string(body)
	}
	if err != nil {
		s.recordAudit(r, action, target, params, false, "数据库操作失败", int(time.Since(start).Milliseconds()))
		s.replyDatabaseError(w, err)
		return
	}
	s.recordAudit(r, action, target, params, true, message, int(time.Since(start).Milliseconds()))
	writeEnvelopeOK(w, result, message)
}

func (s *Server) insertTableRow(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	data, ok := tableRowBody(w, r)
	if !ok {
		return
	}
	table := r.PathValue("table")
	id, err := s.deps.DB.InsertTableRow(r.Context(), table, data)
	var result any
	if id != 0 {
		result = map[string]any{"id": id}
	}
	s.finishTableWrite(w, r, "database.row.create", table, "插入成功", result, data, err, started)
}

func (s *Server) updateTableRow(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	data, ok := tableRowBody(w, r)
	if !ok {
		return
	}
	table, pk := r.PathValue("table"), r.PathValue("pk")
	column, err := s.deps.DB.GetPrimaryKey(r.Context(), table)
	if err == nil {
		if column == "" {
			err = db.ErrNoPrimaryKey
		} else {
			err = s.deps.DB.UpdateTableRow(r.Context(), table, column, pk, data)
		}
	}
	s.finishTableWrite(w, r, "database.row.update", table+"."+column+"="+pk, "更新成功", nil, data, err, started)
}

func (s *Server) deleteTableRow(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	table, pk := r.PathValue("table"), r.PathValue("pk")
	column, err := s.deps.DB.GetPrimaryKey(r.Context(), table)
	if err == nil {
		if column == "" {
			err = db.ErrNoPrimaryKey
		} else {
			var affected int64
			affected, err = s.deps.DB.DeleteTableRow(r.Context(), table, column, pk)
			if err == nil && affected == 0 {
				err = db.ErrRowNotFound
			}
		}
	}
	s.finishTableWrite(w, r, "database.row.delete", table+"."+column+"="+pk, "删除成功", nil, nil, err, started)
}
