# Flink Jobs — Agent Guidelines

## Conventions

Kafka source AND sink tables are registered centrally by `sql_runner.py` — SQL files only define JDBC sinks and INSERT logic. `${ENV_VAR}` placeholders are resolved at runtime.

All INSERT statements are aggregated into a single `statement_set`. Notification sinks must be append-only (no PRIMARY KEY = pure INSERT, not UPSERT).

An Iceberg catalog (Nessie REST) is also registered for querying tiered storage tables.

**SASL JAAS config** must use the shaded class path:
`org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule`

SQL files cannot contain JAAS configs — the `;` in the JAAS string conflicts with `sql_runner.py`'s statement splitting.

## Deployment

Re-apply configmaps after SQL changes: `task k8s:configmaps`

Then delete + re-apply the FlinkDeployment:
```
kubectl delete -f kubernetes/flink-deployment.yaml
kubectl apply -f kubernetes/flink-deployment.yaml
```

**K8s imagePullPolicy:** All local dev FlinkDeployment manifests must use `imagePullPolicy: Never`. OrbStack shares the Docker daemon with K8s — `Never` tells K8s to use locally-built images directly without attempting registry pulls. `IfNotPresent` can silently use stale cached images after rebuilds, and `Always` fails because local images (e.g., `pyflink-custom:1.18.0`) don't exist in any registry.
