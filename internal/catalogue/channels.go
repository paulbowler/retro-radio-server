// SPDX-License-Identifier: GPL-3.0-only
package catalogue

import (
	"context"
	"errors"
	"fmt"
	"retroradio.local/server/internal/model"
)

// Directory offsets count streams. UI offsets count distinct channels; rebuild a
// bounded ordered prefix from cached directory pages so variants cannot reappear.
func (s *Service) channelPage(ctx context.Context, term, country, genre string, offset int, popular bool) (Result, error) {
	if offset < 0 || offset > 1000 {
		return Result{}, errors.New("invalid page")
	}
	all := []model.Candidate{}
	groups := []model.Candidate{}
	seen := map[string]bool{}
	result := Result{}

	for raw := 0; raw <= 1000; raw += PageSize {
		var page Result
		var err error
		if popular {
			page, err = s.rawPopular(ctx, country, raw)
		} else {
			page, err = s.rawSearch(ctx, term, country, genre, raw)
		}
		if err != nil {
			return Result{}, err
		}
		result.Cached = result.Cached || page.Cached
		result.Stale = result.Stale || page.Stale
		if page.Message != "" {
			result.Message = page.Message
		}
		added := 0
		for _, c := range page.Stations {
			if !seen[c.UUID] {
				seen[c.UUID] = true
				all = append(all, c)
				added++
			}
		}
		groups = model.GroupCandidates(all)
		if len(page.Stations) < PageSize || added == 0 {
			break
		}
		if len(groups) > offset+PageSize {
			break
		}
	}
	result.More = len(groups) > offset+PageSize
	end := offset + PageSize
	if end > len(groups) {
		end = len(groups)
	}
	if offset < len(groups) {
		result.Stations = groups[offset:end]
	} else {
		result.Stations = []model.Candidate{}
	}
	// Keep grouped candidate details available to automatic checks and library adds.
	if err := s.Store.CachePut(fmt.Sprintf("channels:v1:%t:%s:%s:%s:%d", popular, term, country, genre, offset), result.Stations); err != nil {
		return Result{}, err
	}
	return result, nil
}
func (s *Service) SearchFiltered(ctx context.Context, term, country, genre string, offset int) (Result, error) {
	return s.channelPage(ctx, term, country, genre, offset, false)
}
func (s *Service) Popular(ctx context.Context, country string, offset int) (Result, error) {
	return s.channelPage(ctx, "", country, "", offset, true)
}
