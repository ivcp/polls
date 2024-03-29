package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivcp/polls/internal/data"
	"github.com/ivcp/polls/internal/validator"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	ctxPollIDKey contextKey = "pollID"
	ctxPollKey   contextKey = "poll"
)

func (app *application) pollIDfromContext(ctx context.Context) string {
	return ctx.Value(ctxPollIDKey).(string)
}

func (app *application) pollFromContext(ctx context.Context) *data.Poll {
	return ctx.Value(ctxPollKey).(*data.Poll)
}

func (app *application) readIDParam(r *http.Request, idKey string) (string, error) {
	param := chi.URLParam(r, idKey)
	if param == "" {
		return "", errors.New("invalid id")
	}
	_, err := uuid.Parse(param)
	if err != nil {
		return "", errors.New("invalid id")
	}
	return param, nil
}

type envelope map[string]any

func (app *application) writeJSON(w http.ResponseWriter, status int, data envelope, headers http.Header) error {
	j, err := json.Marshal(data)
	if err != nil {
		return err
	}
	j = append(j, '\n')

	for key, value := range headers {
		w.Header()[key] = value
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(j)

	return nil
}

func (app *application) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	maxBytes := 1_048_576
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		var maxBytesError *http.MaxBytesError
		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("body contains badly-formed JSON (at character %d)", syntaxError.Offset)
		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly-formed JSON")
		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)
			}
			return fmt.Errorf("body contains incorrect JSON type (at character %d)", unmarshalTypeError.Offset)
		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return fmt.Errorf("body contains unknown key %s", fieldName)

		case errors.As(err, &maxBytesError):
			return fmt.Errorf("body must not be larger than %d bytes", maxBytesError.Limit)
		case errors.As(err, &invalidUnmarshalError):
			panic(err)
		default:
			return err
		}

	}
	err = dec.Decode(&struct{}{})
	if err != io.EOF {
		return errors.New("body must only contain a single JSON value")
	}

	return nil
}

func (app *application) readString(qs url.Values, key string, defaultValue string) string {
	s := qs.Get(key)

	if s == "" {
		return defaultValue
	}

	return s
}

func (app *application) readInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
	s := qs.Get(key)

	if s == "" {
		return defaultValue
	}

	i, err := strconv.Atoi(s)
	if err != nil {
		v.AddError(key, "must be an integer value")
		return defaultValue
	}

	return i
}

func (app *application) checkIP(pollID string, ip string) (bool, error) {
	ips, err := app.models.Polls.GetVotedIPs(pollID)
	if err != nil {
		return false, fmt.Errorf("checkIP %s", err)
	}

	voted := false
	for _, storedIP := range ips {
		if storedIP.Equal(net.ParseIP(ip)) {
			voted = true
		}
	}

	return voted, nil
}

func (app *application) setMetrics(db *pgxpool.Pool) {
	expvar.NewString("version").Set(version)
	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))
	expvar.Publish("database", expvar.Func(func() any {
		return struct {
			MaxConns                int32
			TotalCons               int32
			NewConnsCount           int64
			AcquiredConns           int32
			IdleConns               int32
			MaxIdleDestroyCount     int64
			MaxLifetimeDestroyCount int64
			TotalDuration           time.Duration
			Canceled                int64
			ConstructingConns       int32
			EmptyAcquireCount       int64
		}{
			db.Stat().MaxConns(),
			db.Stat().TotalConns(),
			db.Stat().NewConnsCount(),
			db.Stat().AcquiredConns(),
			db.Stat().IdleConns(),
			db.Stat().MaxIdleDestroyCount(),
			db.Stat().MaxLifetimeDestroyCount(),
			db.Stat().AcquireDuration(),
			db.Stat().CanceledAcquireCount(),
			db.Stat().ConstructingConns(),
			db.Stat().EmptyAcquireCount(),
		}
	}))

	expvar.Publish("timestamp", expvar.Func(func() any {
		return time.Now().Unix()
	}))
}
