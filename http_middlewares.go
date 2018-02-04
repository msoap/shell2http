package main

import "net/http"

// ------------------------------------------------------------------
// mwMethodOnly - allow one HTTP method only
func mwMethodOnly(handler http.HandlerFunc, method string) http.HandlerFunc {
	if method == "" {
		return handler
	}

	return func(rw http.ResponseWriter, req *http.Request) {
		if req.Method == method {
			handler.ServeHTTP(rw, req)
		} else {
			http.Error(rw, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	}
}

// ------------------------------------------------------------------
// mwBasicAuth - add HTTP Basic Authentication
func mwBasicAuth(handler http.HandlerFunc, user, pass string) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		reqUser, reqPass, ok := req.BasicAuth()
		if !ok || reqUser != user || reqPass != pass {
			setCommonHeaders(rw)
			rw.Header().Set("WWW-Authenticate", `Basic realm="Please enter user and passoerd"`)
			http.Error(rw, "name/password is required", http.StatusUnauthorized)
			printAccessLogLine(req)
			return
		}

		handler.ServeHTTP(rw, req)
	}
}
