package service

import "quotes/internal/domain"

// 编排内部的简写均是 Domain 的类型别名；实体只在 Domain 定义，不在服务里复制模型。
type Host = domain.Host
type Security = domain.Security
type Bar = domain.Bar
type AdjustmentEvent = domain.AdjustmentEvent
type DownloadOptions = domain.DownloadOptions
type LogEntry = domain.LogEntry
type FileResult = domain.FileResult
type TaskSnapshot = domain.TaskSnapshot
type checkpoint = domain.Checkpoint
type runEvent = domain.JobEvent
type ingestionPlan = domain.IngestionPlan
type savedRun = domain.SavedRun
