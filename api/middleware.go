package api

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"kvstore/utils"
)

type contextKey string

const requestIDKey contextKey = "request_id"

var requestSeq uint64

func requestIDFromContext(ctx context.Context) string {
	val := ctx.Value(requestIDKey)
	id, _ := val.(string)
	return id
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&requestSeq, 1))
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func panicRecoveryMiddleware(next http.Handler, logger *utils.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if logger != nil {
					reqID := requestIDFromContext(r.Context())
					logger.Printf("[%s] panic recovered: %v", reqID, rec)
				}
				writeJSON(w, http.StatusInternalServerError, apiResponse{
					OK:      false,
					Message: "internal server error",
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}
