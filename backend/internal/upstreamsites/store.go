package upstreamsites

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Record struct {
	Key       string
	ValueJson []byte
	Version   int64
	Found     bool
}
type ListRequest struct {
	Collection, Prefix, Cursor string
	Limit                      int
}
type recordPage struct {
	Items      []*Record
	NextCursor string
}
type writeOptions struct {
	version int64
	ttl     time.Duration
	expires time.Time
}
type PutOption func(*writeOptions)

func IfVersion(version int64) PutOption { return func(o *writeOptions) { o.version = version } }
func IfAbsent() PutOption               { return IfVersion(-1) }
func TTL(ttl time.Duration) PutOption   { return func(o *writeOptions) { o.ttl = ttl } }
func ExpiresAt(at time.Time) PutOption  { return func(o *writeOptions) { o.expires = at } }

type recordStore struct{ db database }

func (s recordStore) Get(ctx context.Context, collection, key string) (*Record, error) {
	r := &Record{Key: key}
	err := s.db.QueryRowContext(ctx, `SELECT value, version FROM upstream_site_records WHERE collection=$1 AND key=$2 AND (expires_at IS NULL OR expires_at>NOW())`, collection, key).Scan(&r.ValueJson, &r.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	r.Found = err == nil
	return r, err
}
func (s recordStore) Put(ctx context.Context, collection, key string, value any, options ...PutOption) (int64, error) {
	o := writeOptions{}
	for _, option := range options {
		option(&o)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	var expires *time.Time
	if !o.expires.IsZero() {
		expires = &o.expires
	} else if o.ttl > 0 {
		t := time.Now().Add(o.ttl)
		expires = &t
	}
	var version int64
	if o.version <= 0 {
		err = s.db.QueryRowContext(ctx, `INSERT INTO upstream_site_records(collection,key,value,expires_at)
 VALUES ($1,$2,$3::jsonb,$4)
 ON CONFLICT(collection,key) DO UPDATE SET value=EXCLUDED.value,expires_at=EXCLUDED.expires_at,version=upstream_site_records.version+1
 WHERE $5::bigint=0 OR upstream_site_records.version=$5
 RETURNING version`, collection, key, string(raw), expires, o.version).Scan(&version)
	} else {
		err = s.db.QueryRowContext(ctx, `UPDATE upstream_site_records SET value=$3::jsonb,expires_at=$4,version=version+1 WHERE collection=$1 AND key=$2 AND version=$5 RETURNING version`, collection, key, string(raw), expires, o.version).Scan(&version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrConflict
	}
	return version, err
}
func (s recordStore) List(ctx context.Context, request *ListRequest) (*recordPage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,value,version FROM upstream_site_records WHERE collection=$1 AND starts_with(key,$2) AND key>$3 AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY key LIMIT $4`, request.Collection, request.Prefix, request.Cursor, request.Limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	page := &recordPage{Items: []*Record{}}
	for rows.Next() {
		r := &Record{Found: true}
		if err = rows.Scan(&r.Key, &r.ValueJson, &r.Version); err != nil {
			return nil, err
		}
		page.Items = append(page.Items, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Items) > request.Limit {
		page.Items = page.Items[:request.Limit]
		page.NextCursor = page.Items[len(page.Items)-1].Key
	}
	return page, nil
}
func (s recordStore) Delete(ctx context.Context, collection, key string, version int64) (bool, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM upstream_site_records WHERE collection=$1 AND key=$2 AND version=$3`, collection, key, version)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
