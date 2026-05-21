package surf

import (
	"encoding/json"
	"net/http"
)

// jsonContentType is the Content-Type set on every JSON response.
const jsonContentType = "application/json; charset=utf-8"

// JSON writes v as a JSON response with the given status code. It sets the
// Content-Type header and returns any encoding error so a handler can simply
// `return surf.JSON(w, 200, v)`.
func JSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// JSONData writes v wrapped in a {"data": ...} envelope with status 200.
func JSONData(w http.ResponseWriter, v any) error {
	return JSON(w, http.StatusOK, dataEnvelope{Data: v})
}

// JSONDataStatus writes v wrapped in a {"data": ...} envelope with a custom
// status code (e.g. 201 Created).
func JSONDataStatus(w http.ResponseWriter, status int, v any) error {
	return JSON(w, status, dataEnvelope{Data: v})
}

// JSONList writes items wrapped in a {"data": [...], "total": n} envelope. Use
// total to report the unpaginated count when items is a page.
func JSONList(w http.ResponseWriter, items any, total int) error {
	return JSON(w, http.StatusOK, listEnvelope{Data: items, Total: total})
}

// JSONError writes a {"error": message, "status": status} envelope with the
// given status code.
func JSONError(w http.ResponseWriter, status int, message string) error {
	return JSON(w, status, errorEnvelope{Error: message, Status: status})
}

type dataEnvelope struct {
	Data any `json:"data"`
}

type listEnvelope struct {
	Data  any `json:"data"`
	Total int `json:"total"`
}

type errorEnvelope struct {
	Error  string `json:"error"`
	Status int    `json:"status"`
}
