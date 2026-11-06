package eoslink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"timesheet-filler/internal/eos"
)

const filterKey = "eos_filter"

// Filter returns the admin's event filter; an unset filter excludes nothing.
func (s *Service) Filter(ctx context.Context) (eos.Filter, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT value FROM app_settings WHERE key = $1", filterKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return eos.Filter{}, nil
	}
	if err != nil {
		return eos.Filter{}, err
	}
	var f eos.Filter
	if err := json.Unmarshal(raw, &f); err != nil {
		return eos.Filter{}, fmt.Errorf("eoslink: stored filter: %w", err)
	}
	return f, nil
}

func (s *Service) SaveFilter(ctx context.Context, adminID int64, f eos.Filter, ip string) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO app_settings (key, value, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3, updated_at = now()`,
		filterKey, raw, adminID); err != nil {
		return err
	}
	var purposes []string
	for _, p := range f.ExcludedPurposes {
		purposes = append(purposes, string(p))
	}
	s.audit(ctx, adminID, "eos.filter_updated",
		fmt.Sprintf("excluded types %v, purposes [%s]", f.ExcludedEventTypes, strings.Join(purposes, " ")), ip)
	return nil
}

// EventTypes lists the club's EOS event types through the user's own link.
func (s *Service) EventTypes(ctx context.Context, userID int64) ([]eos.EventType, error) {
	token, _, err := s.AccessToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	types, err := s.client.EventTypes(ctx, token)
	if errors.Is(err, eos.ErrUnauthorized) {
		if _, err := s.pool.Exec(ctx,
			"UPDATE eos_links SET access_enc = NULL, access_expires_at = NULL WHERE user_id = $1", userID); err != nil {
			return nil, err
		}
		if token, _, err = s.AccessToken(ctx, userID); err != nil {
			return nil, err
		}
		return s.client.EventTypes(ctx, token)
	}
	return types, err
}
