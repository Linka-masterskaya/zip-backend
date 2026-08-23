package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	checkTimeout = 2 * time.Second

	StatusOK       Status = "ok"
	StatusError    Status = "error"
	StatusDegraded Status = "degraded"
	StatusAlive    Status = "alive"
)

// Status represents the health state returned by health endpoints.
type Status string

// Pinger — интерфейс для проверки зависимости через Ping.
type Pinger interface {
	Ping(ctx context.Context) error
}

// ConnectionChecker — интерфейс для проверки состояния подключения.
type ConnectionChecker interface {
	IsConnected() bool
}

type checkResult struct {
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// PicturesBank описывает выбранный источник картинок для отчёта в /readyz.
// Проверка информационная: внешний банк не пингуется, а local mode опирается
// на уже проверяемые PostgreSQL и MinIO зависимости.
type PicturesBank struct {
	Local bool
	URL   string
}

func (b PicturesBank) detail() string {
	if b.Local {
		return "local"
	}
	return "external " + b.URL
}

type check struct {
	detail string
	run    func(context.Context) error
}

type response struct {
	Status Status                 `json:"status"`
	Checks map[string]checkResult `json:"checks"`
}

// Checker содержит клиенты для проверки зависимостей.
type Checker struct {
	db          Pinger
	redisClient Pinger
	natsConn    ConnectionChecker
	minioClient Pinger
	bank        PicturesBank
}

// NewChecker validates health dependencies before endpoint registration.
func NewChecker(db Pinger, redisClient Pinger, natsConn ConnectionChecker, minioClient Pinger, bank PicturesBank) (*Checker, error) {
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
		bank:        bank,
	}, nil
}

// Run запускает параллельные проверки с таймаутом 2 секунды на каждую проверку.
// Возвращает HTTP статус и тело ответа.
func (c *Checker) Run(ctx context.Context) (int, interface{}) {
	checks := c.checks()
	results := make(map[string]checkResult, len(checks))
	var group errgroup.Group
	var mu sync.Mutex

	setResult := func(name, detail string, err error) {
		result := checkResult{Status: StatusOK, Detail: detail}
		if err != nil {
			result = checkResult{Status: StatusError, Detail: detail, Error: err.Error()}
		}
		mu.Lock()
		results[name] = result
		mu.Unlock()
	}

	for name, c := range checks {
		if err := ctx.Err(); err != nil {
			setResult(name, c.detail, err)
		} else {
			group.Go(func() error {
				err := runCheck(ctx, c.run)
				setResult(name, c.detail, err)
				return err
			})
		}
	}

	waitErr := group.Wait()
	status := StatusOK
	httpStatus := http.StatusOK
	if waitErr != nil || hasErrors(results) {
		status = StatusDegraded
		httpStatus = http.StatusServiceUnavailable
	}

	return httpStatus, response{
		Status: status,
		Checks: results,
	}
}

func (c *Checker) checks() map[string]check {
	return map[string]check{
		"postgres": {run: c.db.Ping},
		"redis":    {run: c.redisClient.Ping},
		"nats": {run: func(context.Context) error {
			if !c.natsConn.IsConnected() {
				return errors.New("nats connection is closed")
			}
			return nil
		}},
		"minio": {
			detail: "object storage for media and pack archives",
			run:    c.minioClient.Ping,
		},
		"pictures_bank": {
			detail: c.bank.detail(),
			run:    func(context.Context) error { return nil },
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
		kind == reflect.Pointer ||
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
		if result.Status == StatusError {
			hasError = true
		}
	}
	return hasError
}
