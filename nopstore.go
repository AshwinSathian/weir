package weir

import (
	"context"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// nopStore stores nothing. New uses it when Config.Store is nil until the
// memory store exists (M1-09).
type nopStore struct{}

func (nopStore) Get(context.Context, store.Key) (*store.Entry, error) { return nil, store.ErrNotFound }
func (nopStore) Set(context.Context, store.Key, *store.Entry) error   { return nil }
func (nopStore) Delete(context.Context, store.Key) error              { return nil }
func (nopStore) SetEpoch(context.Context, store.Tag, store.Epoch) error {
	return nil
}
func (nopStore) NewestEpoch(context.Context, []store.Tag, time.Time) (store.Epoch, bool, error) {
	return store.Epoch{}, false, nil
}
func (nopStore) Info() store.Info { return store.Info{Name: "nop"} }
func (nopStore) Close() error     { return nil }
