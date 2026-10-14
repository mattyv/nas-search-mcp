package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type modelSession struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Scanner
	timer      *time.Timer
	generation uint64
}

func modelKeepaliveFromEnv() (time.Duration, error) {
	value := os.Getenv("NAS_MODEL_KEEPALIVE_MINUTES")
	if value == "" {
		return 5 * time.Minute, nil
	}
	minutes, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(minutes) || math.IsInf(minutes, 0) || minutes < 0 || minutes >= float64(math.MaxInt64)/float64(time.Minute) {
		return 0, errors.New("NAS_MODEL_KEEPALIVE_MINUTES must be a finite nonnegative number of minutes")
	}
	return time.Duration(minutes * float64(time.Minute)), nil
}

// stopSession requires sessionMu. Wait reaps the child before another worker starts.
func (a *App) stopSession() {
	s := a.session
	if s == nil {
		return
	}
	a.session = nil
	if s.timer != nil {
		s.timer.Stop()
	}
	s.stdin.Close()
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
}

// Close releases the resident model and the database.
func (a *App) Close() error {
	a.sessionMu.Lock()
	a.closed = true
	a.stopSession()
	a.sessionMu.Unlock()
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}

// sessionRequest runs with both the request gate and sessionMu held.
func (a *App) sessionRequest(ctx context.Context, input []byte) (WorkerResult, error) {
	var result WorkerResult
	if a.session == nil {
		cmd := exec.Command(a.Python, a.Worker, "--serve")
		cmd.Env = append(os.Environ(), "NAS_DATA="+a.Data, "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "TOKENIZERS_PARALLELISM=false")
		cmd.Stderr = io.Discard
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return result, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			stdin.Close()
			return result, err
		}
		if err = cmd.Start(); err != nil {
			stdin.Close()
			return result, err
		}
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		a.session = &modelSession{cmd: cmd, stdin: stdin, stdout: scanner}
	}
	s := a.session
	s.generation++
	generation := s.generation
	if s.timer != nil {
		s.timer.Stop()
	}
	type response struct {
		line []byte
		err  error
	}
	done := make(chan response, 1)
	go func() {
		if _, err := s.stdin.Write(append(input, '\n')); err != nil {
			done <- response{err: err}
			return
		}
		if !s.stdout.Scan() {
			err := s.stdout.Err()
			if err == nil {
				err = io.EOF
			}
			done <- response{err: err}
			return
		}
		done <- response{line: append([]byte(nil), s.stdout.Bytes()...)}
	}()
	select {
	case <-ctx.Done():
		a.stopSession()
		<-done
		return result, ctx.Err()
	case reply := <-done:
		if reply.err != nil {
			a.stopSession()
			return result, fmt.Errorf("worker failed: %w", reply.err)
		}
		if err := json.Unmarshal(reply.line, &result); err != nil {
			a.stopSession()
			return result, fmt.Errorf("invalid worker response: %w", err)
		}
	}
	s.timer = time.AfterFunc(a.modelKeepalive, func() {
		a.sessionMu.Lock()
		defer a.sessionMu.Unlock()
		if a.session == s && s.generation == generation {
			a.stopSession()
		}
	})
	if result.Error != "" {
		return result, errors.New("document/model worker reported an error")
	}
	return result, nil
}
