package monitoring

import (
	"context"

	"github.com/rancher/types/config"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Register initializes the controllers and registers
func Register(ctx context.Context, agentContext *config.UserContext) {
	clusterName := agentContext.ClusterName
	logrus.Infof("Registering monitoring for cluster %q", clusterName)

	cattleContext := agentContext.Management
	mgmtContext := cattleContext.Management
	cattleClustersClient := mgmtContext.Clusters(metav1.NamespaceAll)
	cattleProjectsClient := mgmtContext.Projects(clusterName)

	// app handler
	ah := &appHandler{
		cattleAppClient:             cattleContext.Project.Apps(metav1.NamespaceAll),
		cattleProjectClient:         cattleProjectsClient,
		cattleSecretClient:          cattleContext.Core.Secrets(metav1.NamespaceAll),
		cattleTemplateVersionClient: mgmtContext.CatalogTemplateVersions(metav1.NamespaceAll),
		cattleClusterGraphClient:    mgmtContext.ClusterMonitorGraphs(metav1.NamespaceAll),
		cattleProjectGraphClient:    mgmtContext.ProjectMonitorGraphs(metav1.NamespaceAll),
		cattleMonitorMetricClient:   mgmtContext.MonitorMetrics(metav1.NamespaceAll),
		agentDeploymentClient:       agentContext.Apps.Deployments(metav1.NamespaceAll),
		agentDaemonSetClient:        agentContext.Apps.DaemonSets(metav1.NamespaceAll),
		agentStatefulSetClient:      agentContext.Apps.StatefulSets(metav1.NamespaceAll),
		agentServiceAccountClient:   agentContext.Core.ServiceAccounts(metav1.NamespaceAll),
		agentSecretClient:           agentContext.Core.Secrets(metav1.NamespaceAll),
		agentNodeClient:             agentContext.Core.Nodes(metav1.NamespaceAll),
		agentNamespaceClient:        agentContext.Core.Namespaces(metav1.NamespaceAll),
	}

	// operator handler
	oh := &operatorHandler{
		clusterName:         clusterName,
		cattleClusterClient: cattleClustersClient,
		app:                 ah,
	}
	cattleClustersClient.AddHandler(ctx, "prometheus-operator-handler", oh.sync)

	// cluster handler
	ch := &clusterHandler{
		clusterName:          clusterName,
		cattleClustersClient: cattleClustersClient,
		app:                  ah,
	}
	cattleClustersClient.AddHandler(ctx, "cluster-monitoring-handler", ch.sync)

	// project handler
	ph := &projectHandler{
		clusterName:         clusterName,
		cattleClusterClient: cattleClustersClient,
		cattleProjectClient: cattleProjectsClient,
		app:                 ah,
	}
	cattleProjectsClient.Controller().AddClusterScopedHandler(ctx, "project-monitoring-handler", clusterName, ph.sync)
}
