package handler

import (
	"testing"

	"quotes/internal/repository"
	"quotes/internal/service"
)

func newTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	jobs, err := service.NewJobService(service.JobDependencies{DefaultOutput: dir, Archives: func(output string) service.Archive { return &repository.CSVArchive{Directory: output} }, Factors: repository.FileFactors{}})
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(Dependencies{Jobs: jobs, Workspace: service.NewWorkspaceService(nil, nil), Preview: service.NewPreviewService(jobs.OutputDir, func(output string) service.PreviewRepository { return &repository.CSVArchive{Directory: output} }), Warehouse: service.NewWarehouseService(nil)})
}
