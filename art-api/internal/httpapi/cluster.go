package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type clusterStateRepository interface {
	ListClusterNodes(context.Context, time.Time) ([]domain.ClusterNode, error)
	ListClusterLeases(context.Context, time.Time) ([]domain.ClusterLease, error)
}

func (s *Server) clusterState(response http.ResponseWriter, request *http.Request) {
	repository, ok := s.repository.(clusterStateRepository)
	if !ok {
		writeError(response, http.StatusNotImplemented, "cluster state unavailable")
		return
	}
	now := time.Now().UTC()
	nodes, err := repository.ListClusterNodes(request.Context(), now.Add(-90*time.Second))
	if err != nil {
		writeError(response, 500, "cluster nodes unavailable")
		return
	}
	leases, err := repository.ListClusterLeases(request.Context(), now)
	if err != nil {
		writeError(response, 500, "cluster leases unavailable")
		return
	}
	writeJSON(response, 200, map[string]any{"nodes": nodes, "leases": leases, "generated_at": now})
}
