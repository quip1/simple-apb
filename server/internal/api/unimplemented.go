// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import "context"

// Operations in the v0 contract that are built in Phase 1. Each returns
// ErrNotImplemented (→ 501). Move a method out of this file when you implement it.

func (s *Server) Signup(context.Context, SignupRequestObject) (SignupResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) Login(context.Context, LoginRequestObject) (LoginResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) VerifyTotp(context.Context, VerifyTotpRequestObject) (VerifyTotpResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) EnrollTotp(context.Context, EnrollTotpRequestObject) (EnrollTotpResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) ConfirmTotp(context.Context, ConfirmTotpRequestObject) (ConfirmTotpResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) RefreshTokens(context.Context, RefreshTokensRequestObject) (RefreshTokensResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) Logout(context.Context, LogoutRequestObject) (LogoutResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetMe(context.Context, GetMeRequestObject) (GetMeResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) ListMySessions(context.Context, ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) RevokeMySession(context.Context, RevokeMySessionRequestObject) (RevokeMySessionResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) ListLibrary(context.Context, ListLibraryRequestObject) (ListLibraryResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetBook(context.Context, GetBookRequestObject) (GetBookResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) RemoveFromMyLibrary(context.Context, RemoveFromMyLibraryRequestObject) (RemoveFromMyLibraryResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) RestoreToMyLibrary(context.Context, RestoreToMyLibraryRequestObject) (RestoreToMyLibraryResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) HideInMyLibrary(context.Context, HideInMyLibraryRequestObject) (HideInMyLibraryResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetAuthor(context.Context, GetAuthorRequestObject) (GetAuthorResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetNarrator(context.Context, GetNarratorRequestObject) (GetNarratorResponseObject, error) {
	return nil, ErrNotImplemented
}

func (s *Server) GetSeries(context.Context, GetSeriesRequestObject) (GetSeriesResponseObject, error) {
	return nil, ErrNotImplemented
}

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
