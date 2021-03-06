package namespace

import (
	"fmt"

	"github.com/rancher/norman/api/access"
	"github.com/rancher/norman/httperror"
	"github.com/rancher/norman/store/transform"
	"github.com/rancher/norman/types"
	"github.com/rancher/norman/types/convert"
	"github.com/rancher/norman/types/values"
	"github.com/rancher/rancher/pkg/resourcequota"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	mgmtschema "github.com/rancher/types/apis/management.cattle.io/v3/schema"
	clusterclient "github.com/rancher/types/client/cluster/v3"
	mgmtclient "github.com/rancher/types/client/management/v3"
)

const quotaField = "resourceQuota"

func New(store types.Store) types.Store {
	t := &transform.Store{
		Store: store,
		Transformer: func(apiContext *types.APIContext, schema *types.Schema, data map[string]interface{}, opt *types.QueryOptions) (map[string]interface{}, error) {
			anns, _ := data["annotations"].(map[string]interface{})
			if anns["management.cattle.io/system-namespace"] == "true" {
				return nil, nil
			}
			return data, nil
		},
	}

	return &Store{
		Store: t,
	}
}

type Store struct {
	types.Store
}

func (p *Store) Create(apiContext *types.APIContext, schema *types.Schema, data map[string]interface{}) (map[string]interface{}, error) {
	if _, ok := data["resourceQuota"]; ok {
		values.PutValue(data, "{\"conditions\": [{\"type\": \"InitialRolesPopulated\", \"status\": \"Unknown\", \"message\": \"Populating initial roles\"},{\"type\": \"ResourceQuotaValidated\", \"status\": \"Unknown\", \"message\": \"Validating resource quota\"}]}",
			"annotations", "cattle.io/status")
	} else {
		values.PutValue(data, "{\"conditions\": [{\"type\": \"InitialRolesPopulated\", \"status\": \"Unknown\", \"message\": \"Populating initial roles\"}]}",
			"annotations", "cattle.io/status")
	}

	if err := p.validateResourceQuota(apiContext, schema, data, ""); err != nil {
		return nil, err
	}

	return p.Store.Create(apiContext, schema, data)
}

func (p *Store) Update(apiContext *types.APIContext, schema *types.Schema, data map[string]interface{}, id string) (map[string]interface{}, error) {
	if err := p.validateResourceQuota(apiContext, schema, data, id); err != nil {
		return nil, err
	}

	return p.Store.Update(apiContext, schema, data, id)
}

func (p *Store) validateResourceQuota(apiContext *types.APIContext, schema *types.Schema, data map[string]interface{}, id string) error {
	quota := data[quotaField]
	var project mgmtclient.Project
	if err := access.ByID(apiContext, &mgmtschema.Version, mgmtclient.ProjectType, convert.ToString(data["projectId"]), &project); err != nil {
		return err
	}
	if project.ResourceQuota == nil {
		return nil
	}
	var nsQuota mgmtclient.NamespaceResourceQuota
	if quota == nil {
		if project.NamespaceDefaultResourceQuota == nil {
			return nil
		}
		nsQuota = *project.NamespaceDefaultResourceQuota
	} else {
		if err := convert.ToObj(quota, &nsQuota); err != nil {
			return err
		}
	}

	projectQuotaLimit, err := limitToLimit(project.ResourceQuota.Limit)
	if err != nil {
		return err
	}
	nsQuotaLimit, err := limitToLimit(nsQuota.Limit)
	if err != nil {
		return err
	}

	var nsLimits []*v3.ResourceQuotaLimit
	var namespaces []clusterclient.Namespace
	if err := access.List(apiContext, &schema.Version, clusterclient.NamespaceType, &types.QueryOptions{}, &namespaces); err != nil {
		return err
	}
	for _, n := range namespaces {
		if n.ProjectID != data["projectId"] {
			continue
		}
		if n.ResourceQuota == nil {
			continue
		}
		nsLimit, err := limitToLimitCluster(n.ResourceQuota.Limit)
		if err != nil {
			return err
		}
		nsLimits = append(nsLimits, nsLimit)
	}

	isFit, msg, err := resourcequota.IsQuotaFit(nsQuotaLimit, nsLimits, projectQuotaLimit)
	if err != nil || isFit {
		return err
	}

	return httperror.NewFieldAPIError(httperror.MaxLimitExceeded, quotaField, fmt.Sprintf("Resource quota exceeds the project: %s", msg))
}

func limitToLimit(from *mgmtclient.ResourceQuotaLimit) (*v3.ResourceQuotaLimit, error) {
	var to v3.ResourceQuotaLimit
	err := convert.ToObj(from, &to)
	return &to, err
}

func limitToLimitCluster(from *clusterclient.ResourceQuotaLimit) (*v3.ResourceQuotaLimit, error) {
	var to v3.ResourceQuotaLimit
	err := convert.ToObj(from, &to)
	return &to, err
}
