package cache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type regressionAudit59 struct {
	keys            []string
	data            map[string]string
	getErr          map[string]error
	keysErr, delErr error
	deleted         []string
	pattern         string
}

func (a *regressionAudit59) Get(_ context.Context, k string) (string, error) {
	return a.data[k], a.getErr[k]
}
func (a *regressionAudit59) Set(context.Context, string, string, time.Duration) error { return nil }
func (a *regressionAudit59) Del(_ context.Context, k ...string) error {
	a.deleted = append(a.deleted, k...)
	return a.delErr
}
func (a *regressionAudit59) Keys(_ context.Context, p string) ([]string, error) {
	a.pattern = p
	return a.keys, a.keysErr
}
func (a *regressionAudit59) Close() error { return nil }
func TestRedisPurgeByTagReadErrors(t *testing.T) {
	ioerr := errors.New("fixture read failure")
	for _, c := range []struct {
		name                             string
		getErr, keysErr, delErr, wantErr error
		wantDel                          []string
	}{{"ordinary", nil, nil, nil, nil, []string{"p:match"}}, {"missing", fmt.Errorf("wrapped: %w", ErrRedisNotFound), nil, nil, nil, nil}, {"read failure", ioerr, nil, nil, ioerr, nil}, {"keys failure", nil, ioerr, nil, ioerr, nil}, {"delete failure", nil, nil, ioerr, ioerr, []string{"p:match"}}} {
		a := &regressionAudit59{keys: []string{"p:other", "p:bad", "p:match"}, data: map[string]string{"p:other": `{"Tags":["other"]}`, "p:bad": "non-cache", "p:match": `{"Tags":["news","news"]}`}, getErr: map[string]error{"p:match": c.getErr}, keysErr: c.keysErr, delErr: c.delErr}
		r := &RedisCache{client: a, prefix: "p"}
		e := r.PurgeByTag("news")
		if !errors.Is(e, c.wantErr) || !reflect.DeepEqual(a.deleted, c.wantDel) || a.pattern != "p:*" {
			t.Errorf("%s: err=%v deleted=%v pattern=%q", c.name, e, a.deleted, a.pattern)
		}
	}
	var r *RedisCache
	if e := r.PurgeByTag("news"); e != nil {
		t.Fatal(e)
	}
	if !t.Failed() {
		fmt.Println("FIX VERIFIED")
	}
}
