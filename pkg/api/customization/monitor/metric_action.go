package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rancher/norman/parse"
	"github.com/rancher/norman/types"
	"github.com/rancher/norman/types/convert"
	"github.com/rancher/rancher/pkg/clustermanager"
	monitorutil "github.com/rancher/rancher/pkg/monitoring"
	"github.com/rancher/rancher/pkg/ref"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/rancher/types/config/dialer"
	"k8s.io/apimachinery/pkg/util/sets"
)

func NewMetricHandler(dialerFactory dialer.Factory, clustermanager *clustermanager.Manager) *MetricHandler {
	return &MetricHandler{
		dialerFactory:  dialerFactory,
		clustermanager: clustermanager,
	}
}

type MetricHandler struct {
	dialerFactory  dialer.Factory
	clustermanager *clustermanager.Manager
}

func (h *MetricHandler) Action(actionName string, action *types.Action, apiContext *types.APIContext) error {
	switch actionName {
	case querycluster, queryproject:
		var clusterName, projectName, appName, saNamespace string
		var comm v3.CommonQueryMetricInput
		var err error
		var svcNamespace, svcName string

		if actionName == querycluster {
			var queryMetricInput v3.QueryClusterMetricInput
			actionInput, err := parse.ReadBody(apiContext.Request)
			if err != nil {
				return err
			}

			if err = convert.ToObj(actionInput, &queryMetricInput); err != nil {
				return err
			}

			clusterName = queryMetricInput.ClusterName
			if clusterName == "" {
				return fmt.Errorf("clusterName is empty")
			}

			comm = queryMetricInput.CommonQueryMetricInput
			appName, saNamespace = monitorutil.ClusterMonitoringInfo()
			svcName, svcNamespace, _ = monitorutil.ClusterPrometheusEndpoint()
		} else {
			var queryMetricInput v3.QueryProjectMetricInput
			actionInput, err := parse.ReadBody(apiContext.Request)
			if err != nil {
				return err
			}
			if err = convert.ToObj(actionInput, &queryMetricInput); err != nil {
				return err
			}

			projectID := queryMetricInput.ProjectName
			clusterName, projectName = ref.Parse(projectID)

			if clusterName == "" {
				return fmt.Errorf("clusterName is empty")
			}

			comm = queryMetricInput.CommonQueryMetricInput
			appName, saNamespace = monitorutil.ProjectMonitoringInfo(projectName)
			svcName, svcNamespace = monitorutil.ProjectPrometheusServiceInfo(projectName)
		}

		start, end, step, err := parseTimeParams(comm.From, comm.To, comm.Interval)
		if err != nil {
			return err
		}

		userContext, err := h.clustermanager.UserContext(clusterName)
		if err != nil {
			return fmt.Errorf("get usercontext failed, %v", err)
		}

		token, err := getAuthToken(userContext, appName, saNamespace)
		if err != nil {
			return err
		}

		reqContext, cancel := context.WithTimeout(context.Background(), prometheusReqTimeout)
		defer cancel()

		prometheusQuery, err := NewPrometheusQuery(reqContext, userContext, clusterName, token, svcNamespace, svcName, h.clustermanager, h.dialerFactory)
		if err != nil {
			return err
		}

		query := InitPromQuery("", start, end, step, comm.Expr, "", false)
		seriesSlice, err := prometheusQuery.QueryRange(query)
		if err != nil {
			return err
		}

		if seriesSlice == nil {
			apiContext.WriteResponse(http.StatusNoContent, nil)
			return nil
		}

		data := map[string]interface{}{
			"type":   "queryMetricOutput",
			"series": seriesSlice,
		}

		res, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal query stats result failed, %v", err)
		}
		apiContext.Response.Write(res)

	case listclustermetricname:
		var input v3.ClusterMetricNamesInput
		actionInput, err := parse.ReadBody(apiContext.Request)
		if err != nil {
			return err
		}
		if err = convert.ToObj(actionInput, &input); err != nil {
			return err
		}

		clusterName := input.ClusterName
		if clusterName == "" {
			return fmt.Errorf("clusterName is empty")
		}

		userContext, err := h.clustermanager.UserContext(clusterName)
		if err != nil {
			return fmt.Errorf("get usercontext failed, %v", err)
		}

		appName, saNamespace := monitorutil.ClusterMonitoringInfo()
		token, err := getAuthToken(userContext, appName, saNamespace)
		if err != nil {
			return err
		}

		reqContext, cancel := context.WithTimeout(context.Background(), prometheusReqTimeout)
		defer cancel()

		svcName, svcNamespace, _ := monitorutil.ClusterPrometheusEndpoint()
		prometheusQuery, err := NewPrometheusQuery(reqContext, userContext, clusterName, token, svcNamespace, svcName, h.clustermanager, h.dialerFactory)
		if err != nil {
			return err
		}

		names, err := prometheusQuery.GetLabelValues("__name__")
		if err != nil {
			return err
		}
		data := map[string]interface{}{
			"type":  "metricNamesOutput",
			"names": names,
		}

		apiContext.WriteResponse(http.StatusOK, data)
	case listprojectmetricname:
		// project metric names need to merge cluster level and project level prometheus labels name list
		var input v3.ProjectMetricNamesInput
		actionInput, err := parse.ReadBody(apiContext.Request)
		if err != nil {
			return err
		}
		if err = convert.ToObj(actionInput, &input); err != nil {
			return err
		}

		projectID := input.ProjectName
		clusterName, projectName := ref.Parse(projectID)

		if clusterName == "" {
			return fmt.Errorf("clusterName is empty")
		}

		userContext, err := h.clustermanager.UserContext(clusterName)
		if err != nil {
			return fmt.Errorf("get usercontext failed, %v", err)
		}

		appName, saNamespace := monitorutil.ProjectMonitoringInfo(projectName)
		token, err := getAuthToken(userContext, appName, saNamespace)
		if err != nil {
			return err
		}

		reqContext, cancel := context.WithTimeout(context.Background(), prometheusReqTimeout)
		defer cancel()

		// get name list for user definded metric from project prometheus
		projectSvcName, projectSvcNamespace := monitorutil.ProjectPrometheusServiceInfo(projectName)
		projectPrometheusQuery, err := NewPrometheusQuery(reqContext, userContext, clusterName, token, projectSvcNamespace, projectSvcName, h.clustermanager, h.dialerFactory)
		if err != nil {
			return err
		}
		projectNames, err := projectPrometheusQuery.GetLabelValues("__name__")
		if err != nil {
			return fmt.Errorf("get project metric list failed, %v", err)
		}

		// get name list for pod/container metric from cluster prometheus
		clusterSvcName, clusterSvcNamespace, _ := monitorutil.ClusterPrometheusEndpoint()
		clusterPrometheusQuery, err := NewPrometheusQuery(reqContext, userContext, clusterName, token, clusterSvcNamespace, clusterSvcName, h.clustermanager, h.dialerFactory)
		if err != nil {
			return err
		}

		clusterNames, err := clusterPrometheusQuery.GetLabelValues("__name__")
		if err != nil {
			return fmt.Errorf("get cluster metric list failed, %v", err)
		}

		names := sets.String{}
		names.Insert(projectNames...)
		names.Insert(clusterNames...)

		data := map[string]interface{}{
			"type":  "metricNamesOutput",
			"names": names.List(),
		}
		apiContext.WriteResponse(http.StatusOK, data)
	}
	return nil

}
