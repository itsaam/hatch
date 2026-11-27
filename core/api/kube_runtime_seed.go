package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

func (k *kubeRuntime) seedPostgres(ctx context.Context, slot *kubeSlot, plan kubeServicePlan, path string, seed func(context.Context, string) ([]byte, error)) error {
	if seed == nil {
		return errors.New("no seed file loader")
	}
	if kubeDependencyPort(plan.name, plan.image) != 5432 && plan.port != 5432 && plan.service.Env["POSTGRES_PASSWORD"] == "" {
		return errors.New("seed target is not recognized as PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(ctx, kubeReadyTimeout)
	defer cancel()

	sql, err := seed(ctx, path)
	if err != nil {
		return errors.New("could not load seed SQL")
	}
	if len(sql) == 0 || len(sql) > kubeMaxSecretData {
		return errors.New("seed SQL is missing or exceeds size limit")
	}
	sqlSecret := "sql.seed." + slot.claim.Metadata.UID
	if err := k.createSecret(ctx, slot, sqlSecret, map[string][]byte{"seed.sql": sql}, kubeSeedPendingName); err != nil {
		return err
	}

	env := []kubeFields{
		{"name": "PGHOST", "value": plan.name},
		{"name": "PGPORT", "value": strconv.Itoa(plan.port)},
		{"name": "PGCONNECT_TIMEOUT", "value": "5"},
		kubeSeedEnv(slot, plan, "PGPASSWORD", "POSTGRES_PASSWORD", ""),
		kubeSeedEnv(slot, plan, "PGUSER", "POSTGRES_USER", "postgres"),
	}
	dbKey := "POSTGRES_DB"
	if plan.service.Env[dbKey] == "" {
		dbKey = "POSTGRES_USER"
	}
	env = append(env, kubeSeedEnv(slot, plan, "PGDATABASE", dbKey, "postgres"))

	security := kubeContainerSecurity("ALL")
	security["runAsNonRoot"] = true
	security["runAsUser"] = 65534
	security["runAsGroup"] = 65534
	security["readOnlyRootFilesystem"] = true
	container := kubeFields{
		"name": "psql", "image": "postgres:17-alpine", "imagePullPolicy": "Always",
		"command": []string{"sh", "-ec"},
		"args": []string{`for i in $(seq 1 20); do
  if pg_isready -q -t 2; then
    exec psql --no-psqlrc --no-password --set=ON_ERROR_STOP=1 --file=/seed/seed.sql
  fi
  sleep 2
done
echo 'PostgreSQL service did not accept connections' >&2
exit 1`},
		"env": env, "resources": kubeResources(250, 128<<20), "securityContext": security,
		"volumeMounts": []kubeFields{{"name": "seed-sql", "mountPath": "/seed", "readOnly": true}},
	}
	pod := k.podSpec(container)
	pod["restartPolicy"] = "Never"
	pod["volumes"] = []kubeFields{{"name": "seed-sql", "secret": kubeFields{"secretName": sqlSecret}}}
	job := kubeObject("batch/v1", "Job", slot.metadata(kubeSeedName, slot.labels()), "spec", kubeFields{
		// Keep the bounded Job object until cleanup can confirm its creation.
		"backoffLimit": 0, "activeDeadlineSeconds": 90,
		"template": kubeFields{
			"metadata": kubeFields{"labels": slot.labels()},
			"spec":     pod,
		},
	})
	if err := k.createOwned(ctx, slot, kubeJobs, kubeSeedName, job, kubeSeedPendingName); err != nil {
		return err
	}
	return k.waitSeedJob(ctx, slot)
}

func kubeSeedEnv(slot *kubeSlot, plan kubeServicePlan, envName, secretKey, fallback string) kubeFields {
	if plan.service.Env[secretKey] == "" {
		return kubeFields{"name": envName, "value": fallback}
	}
	return kubeFields{
		"name":      envName,
		"valueFrom": kubeFields{"secretKeyRef": kubeFields{"name": slot.envSecretName(plan.name), "key": secretKey}},
	}
}

func (k *kubeRuntime) waitSeedJob(ctx context.Context, slot *kubeSlot) error {
	path := kubeJobs.path(slot.namespace, kubeSeedName)
	return waitKube(ctx, func() (bool, error) {
		var job struct {
			Metadata kubeMeta `json:"metadata"`
			Status   struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := k.client.json(ctx, http.MethodGet, path, nil, &job); err != nil {
			return false, err
		}
		if !slot.ownsDirectly(job.Metadata) || job.Metadata.DeletionTimestamp != nil {
			return false, errors.New("kubernetes seed job changed while running")
		}
		for _, condition := range job.Status.Conditions {
			if condition.Status != "True" {
				continue
			}
			switch condition.Type {
			case "Complete":
				return true, nil
			case "Failed":
				return false, errors.New("kubernetes PostgreSQL seed job failed")
			}
		}
		return false, nil
	})
}
