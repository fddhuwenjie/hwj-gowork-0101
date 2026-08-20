// Package app 负责组装进程：配置、数据库、服务、HTTP 与后台任务调度器，
// 并处理信号驱动的优雅关闭。
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"metalmics/internal/config"
	"metalmics/internal/httpapi"
	"metalmics/internal/logging"
	"metalmics/internal/repository"
	"metalmics/internal/service"
)

// Run 是进程主入口：初始化依赖、启动 HTTP 服务与后台任务调度器，
// 收到 SIGINT/SIGTERM 后优雅关闭。返回进程退出码。
func Run() int {
	logger := logging.New()
	cfg, err := config.Load()
	if err != nil {
		logger.Error("配置加载失败", "error", err)
		return 1
	}
	ctx := context.Background()
	db, err := repository.Open(ctx, cfg.DBPath)
	if err != nil {
		logger.Error("数据库初始化失败", "error", err, "db_path", cfg.DBPath)
		return 1
	}
	defer db.Close()

	store := service.NewStore(db)
	daily := service.NewDailyService(store)
	exception := service.NewExceptionService(store)
	review := service.NewReviewService(store)
	report := service.NewReportService(store)
	jobs := service.NewJobService(store)

	// 重启恢复：把上次进程遗留的 running 任务重新排队
	recovered, err := jobs.RecoverOnStartup(ctx)
	if err != nil {
		logger.Error("后台任务恢复失败", "error", err)
		return 1
	}
	if recovered > 0 {
		logger.Info("已恢复遗留后台任务", "count", recovered)
	}

	handlers := httpapi.NewHandlers(daily, exception, review, report, jobs, db)
	srv := httpapi.NewServer(fmt.Sprintf(":%d", cfg.Port), httpapi.NewRouter(handlers, logger))

	scheduler := NewJobScheduler(jobs, logger, 500*time.Millisecond)
	scheduler.Start()
	defer scheduler.Stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("服务已启动", "port", cfg.Port, "db_path", cfg.DBPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("收到退出信号，开始优雅关闭", "signal", sig.String())
	case err := <-errCh:
		logger.Error("HTTP 服务异常", "error", err)
		return 1
	}

	scheduler.Stop()
	if err := srv.Shutdown(10 * time.Second); err != nil {
		logger.Error("HTTP 优雅关闭失败", "error", err)
		return 1
	}
	logger.Info("服务已关闭")
	return 0
}

// JobScheduler 周期性地领取并执行到期的持久化后台任务。
type JobScheduler struct {
	jobs     *service.JobService
	logger   *slog.Logger
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

// NewJobScheduler 构造后台任务调度器。
func NewJobScheduler(jobs *service.JobService, logger *slog.Logger, interval time.Duration) *JobScheduler {
	return &JobScheduler{
		jobs:     jobs,
		logger:   logger,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start 启动调度循环。
func (s *JobScheduler) Start() {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.tick()
			}
		}
	}()
}

// Stop 停止调度循环并等待其退出。
func (s *JobScheduler) Stop() {
	close(s.stop)
	<-s.done
}

// tick 连续执行到期任务直到没有可执行任务。
func (s *JobScheduler) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		ran, err := s.jobs.RunDue(ctx)
		if err != nil {
			s.logger.Error("后台任务执行失败", "error", err)
			return
		}
		if !ran {
			return
		}
	}
}
