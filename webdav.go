// Copyright 2015 Matthew Holt
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package webdav implements a WebDAV server handler module for Caddy.
//
// Derived from work by Henrique Dias: https://github.com/hacdias/caddy-webdav
package webdav

import (
	"context"
	"net/http"
	"os"
	"path"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"golang.org/x/net/webdav"
)

func init() {
	caddy.RegisterModule(WebDAV{})
}

// WebDAV implements an HTTP handler for responding to WebDAV clients.
type WebDAV struct {
	// The root directory out of which to serve files. If
	// not specified, `{http.vars.root}` will be used if
	// set; otherwise, the current directory is assumed.
	// Accepts placeholders.
	Root string `json:"root,omitempty"`

	// The base path prefix used to access the WebDAV share.
	// Should be used if one more more matchers are used with the
	// webdav directive and it's needed to let the webdav share know
	// what the request base path will be.
	// For example:
	// webdav /some/path/match/* {
	//   root /path
	//   prefix /some/path/match
	// }
	// Accepts placeholders.
	Prefix string `json:"prefix,omitempty"`

	lockSystem webdav.LockSystem
	logger     *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (WebDAV) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.webdav",
		New: func() caddy.Module { return new(WebDAV) },
	}
}

// Provision sets up the module.
func (wd *WebDAV) Provision(ctx caddy.Context) error {
	wd.logger = ctx.Logger(wd)

	wd.lockSystem = webdav.NewMemLS()
	if wd.Root == "" {
		wd.Root = "{http.vars.root}"
	}

	return nil
}

func (wd WebDAV) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// TODO: integrate with caddy 2's existing auth features to enforce read-only?
	// read methods: GET, HEAD, OPTIONS
	// write methods: POST, PUT, PATCH, DELETE, COPY, MKCOL, MOVE, PROPPATCH

	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	root := repl.ReplaceAll(wd.Root, ".")
	prefix := repl.ReplaceAll(wd.Prefix, "")

	wdHandler := webdav.Handler{
		Prefix:     prefix,
		FileSystem: webdav.Dir(root),
		LockSystem: wd.lockSystem,
		Logger: func(req *http.Request, err error) {
			if err != nil {
				wd.logger.Error("internal handler error",
					zap.Error(err),
					zap.Object("request", caddyhttp.LoggableHTTPRequest{Request: req}),
				)
			}
		},
	}

	// Excerpt from RFC4918, section 9.4:
	//
	//     GET, when applied to a collection, may return the contents of an
	//     "index.html" resource, a human-readable view of the contents of
	//     the collection, or something else altogether.
	//
	// GET and HEAD, when applied to a collection, will behave the same as PROPFIND method.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		info, err := wdHandler.FileSystem.Stat(context.TODO(), r.URL.Path)
		if err == nil && info.IsDir() {
			r.Method = "PROPFIND"
			if r.Header.Get("Depth") == "" {
				r.Header.Add("Depth", "1")
			}
		}
	} else if r.Method == http.MethodPut || r.Method == http.MethodPost {
		// Extract the directory part of the file path
		dirPath := path.Dir(r.URL.Path)
		// Check if the directory exists, if not create it
		_, err := os.Stat(root + dirPath)
		if os.IsNotExist(err) {
			// Create all directories in the path
			if err := os.MkdirAll(root+dirPath, 0755); err != nil {
				wd.logger.Error("error creating directories", zap.Error(err))
				http.Error(w, "Error creating directories", http.StatusInternalServerError)
				return err
			}
		}
	}

	if r.Method == http.MethodHead {
		w = emptyBodyResponseWriter{w}
	}

	wdHandler.ServeHTTP(w, r)

	return nil
}

// emptyBodyResponseWriter is a response writer that does not write a body.
type emptyBodyResponseWriter struct{ http.ResponseWriter }

func (w emptyBodyResponseWriter) Write(data []byte) (int, error) { return 0, nil }

// Interface guards
var (
	_ caddyhttp.MiddlewareHandler = (*WebDAV)(nil)
	_ caddyfile.Unmarshaler       = (*WebDAV)(nil)
)
