// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import "context"

// Operations in the v0 contract that are built in Phase 1. Each returns
// ErrNotImplemented (→ 501). Move a method out of this file when you implement it.

func (s *Server) GetProgress(context.Context, GetProgressRequestObject) (GetProgressResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) PutProgress(context.Context, PutProgressRequestObject) (PutProgressResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetSyncDelta(context.Context, GetSyncDeltaRequestObject) (GetSyncDeltaResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetBookFile(context.Context, GetBookFileRequestObject) (GetBookFileResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetHlsPlaylist(context.Context, GetHlsPlaylistRequestObject) (GetHlsPlaylistResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetHlsSegment(context.Context, GetHlsSegmentRequestObject) (GetHlsSegmentResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) CreateStreamUrl(context.Context, CreateStreamUrlRequestObject) (CreateStreamUrlResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetBookCover(context.Context, GetBookCoverRequestObject) (GetBookCoverResponseObject, error) {
	return nil, ErrNotImplemented
}
