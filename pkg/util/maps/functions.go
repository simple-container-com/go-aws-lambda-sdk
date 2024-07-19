package maps

import (
	"sync"

	"golang.org/x/sync/errgroup"
)

// MapErr is similar to lo.Map, but handles error in iteratee function
func MapErr[T any, R any](collection []T, iteratee func(T, int) (R, error)) ([]R, error) {
	result := make([]R, len(collection))

	for i, item := range collection {
		res, err := iteratee(item, i)
		if err != nil {
			return nil, err
		}
		result[i] = res
	}

	return result, nil
}

func MapParallelErr[T any, R any](collection []T, iteratee func(T, int) (R, error)) ([]R, error) {
	syncStore := sync.Map{}
	errG := errgroup.Group{}

	for i, item := range collection {
		errG.Go(func() error {
			res, err := iteratee(item, i)
			if err != nil {
				return err
			}
			syncStore.Store(i, res)
			return nil
		})
	}

	err := errG.Wait()
	if err != nil {
		return nil, err
	}

	result := make([]R, 0)
	syncStore.Range(func(key, value any) bool {
		result = append(result, value.(R))
		return true
	})

	return result, nil
}
