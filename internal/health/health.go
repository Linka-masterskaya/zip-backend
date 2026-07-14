package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"golang.org/x/sync/errgroup"
)

const checkTimeout = 2 * time.Second

// Pinger — интерфейс для проверки зависимости через Ping.
type Pinger interface {
	Ping(ctx context.Context) error
}

// ConnectionChecker — интерфейс для проверки состояния подключения.
type ConnectionChecker interface {
	IsConnected() bool
}

// Lister — интерфейс для проверки MinIO.
type Lister interface {
	ListBuckets(ctx context.Context) ([]minio.BucketInfo, error)
}

type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type response struct {
	Status string                 `json:"status"`
	Checks map[string]checkResult `json:"checks"`
}

// Checker содержит клиенты для проверки зависимостей.
type Checker struct {
	db          Pinger
	redisClient Pinger
	natsConn    ConnectionChecker
	minioClient Lister
}

// NewChecker validates health dependencies before endpoint registration.
func NewChecker(db Pinger, redisClient Pinger, natsConn ConnectionChecker, minioClient Lister) (*Checker, error) {
	if isNilDependency(db) {
		return nil, errors.New("postgres client not initialized")
	}
	if isNilDependency(redisClient) {
		return nil, errors.New("redis client not initialized")
	}
	if isNilDependency(natsConn) {
		return nil, errors.New("nats client not initialized")
	}
	if isNilDependency(minioClient) {
		return nil, errors.New("minio client not initialized")
	}

	return &Checker{
		db:          db,
		redisClient: redisClient,
		natsConn:    natsConn,
		minioClient: minioClient,
	}, nil
}

// Run запускает параллельные проверки с таймаутом 2 секунды на каждую проверку.
// Возвращает HTTP статус и тело ответа.
func (c *Checker) Run(ctx context.Context) (int, interface{}) {
	checks := c.checks()
	results := make(map[string]checkResult, len(checks))
	var group errgroup.Group
	var mu sync.Mutex

	setResult := func(name string, err error) {
		result := checkResult{Status: "ok"}
		if err != nil {
			result = checkResult{Status: "error", Error: err.Error()}
		}
		mu.Lock()
		results[name] = result
		mu.Unlock()
	}

	for name, check := range checks {
		if err := ctx.Err(); err != nil {
			setResult(name, err)
		} else {
			group.Go(func() error {
				err := runCheck(ctx, check)
				setResult(name, err)
				return err
			})
		}
	}

	waitErr := group.Wait()
	status := "ok"
	httpStatus := http.StatusOK
	if waitErr != nil || hasErrors(results) {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	return httpStatus, response{
		Status: status,
		Checks: results,
	}
}

func (c *Checker) checks() map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"postgres": c.db.Ping,
		"redis":    c.redisClient.Ping,
		"nats": func(context.Context) error {
			if !c.natsConn.IsConnected() {
				return errors.New("nats connection is closed")
			}
			return nil
		},
		"minio": func(ctx context.Context) error {
			_, err := c.minioClient.ListBuckets(ctx)
			return err
		},
	}
}

func isNilDependency(dependency any) bool {
	if dependency == nil {
		return true
	}

	value := reflect.ValueOf(dependency)
	kind := value.Kind()

	nilable := kind == reflect.Chan ||
		kind == reflect.Func ||
		kind == reflect.Interface ||
		kind == reflect.Map ||
		kind == reflect.Ptr ||
		kind == reflect.Slice

	return nilable && value.IsNil()
}

func runCheck(ctx context.Context, check func(context.Context) error) (err error) {
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()

	return check(checkCtx)
}

func hasErrors(results map[string]checkResult) bool {
	hasError := false
	for _, result := range results {
		if result.Status == "error" {
			hasError = true
		}
	}
	return hasError
}
