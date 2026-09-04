package cli

import "github.com/joaomdsg/shedit/internal/store"

func openStore(path string) (*store.Store, error) { return store.Open(path) }
