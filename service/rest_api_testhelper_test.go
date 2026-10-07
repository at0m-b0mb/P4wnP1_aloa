package service

import (
	"net/http"
	"net/http/httptest"
)

func newTestRequest(origin, host string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/rpc/GetDeviceState", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}
