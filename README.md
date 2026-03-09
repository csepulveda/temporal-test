# Temporal Conciliation PoC

Task server centralizado que orquesta conciliaciones de partners con recursos dinamicos en Kubernetes, usando Temporal como motor de workflows.

## Indice

- [Arquitectura](#arquitectura)
- [Componentes](#componentes)
- [Estructura del proyecto](#estructura-del-proyecto)
- [Donde se guardan los datos](#donde-se-guardan-los-datos)
- [Flujo de una conciliacion](#flujo-de-una-conciliacion)
- [Concurrencia por tier](#concurrencia-por-tier)
- [Como disparar una conciliacion](#como-disparar-una-conciliacion)
- [Backoffice UI](#backoffice-ui)
- [Como extender con un nuevo job](#como-extender-con-un-nuevo-job)
- [Deployment](#deployment)
- [Monitoreo](#monitoreo)
- [Stack](#stack)

## Arquitectura

```
                                ┌──────────────────┐
  POST /api/v1/conciliation ───►│                  │
  (webhook directo a Temporal)  │  Trigger Service │──► Backoffice UI (/)
                                │  cmd/trigger     │
  SNS Topic ──► SQS Queue ────►│  SQS Listener    │
                                └────────┬─────────┘
                                         │ StartWorkflow()
                                         ▼
                                ┌──────────────────┐
                                │  Temporal Server  │──► PostgreSQL (temporal, temporal_visibility)
                                │  auto-setup       │
                                └────────┬─────────┘
                                         │ Task Queue: "conciliation"
                                         ▼
                                ┌──────────────────┐
                                │   Task Worker     │
                                │   cmd/worker      │
                                │                   │
                                │  1. GetStats()    │ ← COUNT(*) transacciones pendientes
                                │  2. CalcResources │ ← Decide CPU/RAM segun volumen
                                │  3. LaunchJob()   │ ← Crea K8s Job dinamico
                                └────────┬─────────┘
                                         │ K8s API (batch/v1 Jobs)
                                         ▼
                              ┌────────────────────────┐
                              │  K8s Job (dinamico)     │
                              │  cmd/conciliation-worker│
                              │  CPU y RAM segun carga  │
                              │                         │
                              │  N goroutines paralelas │
                              │  (1 por merchant)       │
                              │                         │
                              │  Por cada transaccion:  │
                              │  SELECT FOR UPDATE loan │
                              │  → calcular retencion   │
                              │  → INSERT collection    │
                              │  → UPDATE loan balance  │
                              │  → UPDATE txn status    │
                              │  → COMMIT atomico       │
                              └────────────────────────┘
                                         │
                                         ▼
                              ┌────────────────────────┐
                              │  PostgreSQL (CNPG)      │
                              │  DB: conciliation       │
                              └────────────────────────┘
```

## Componentes

### 1. Trigger Service (`cmd/trigger/`)

Punto de entrada al sistema. Tiene tres responsabilidades:

- **HTTP API**: Recibe webhooks en `POST /api/v1/conciliation` y arranca workflows en Temporal.
- **SQS Listener**: Poll continuo a una cola SQS (suscrita a un topic SNS). Detecta automaticamente si el mensaje viene envuelto en un envelope SNS o es un mensaje SQS directo.
- **Backoffice UI**: Sirve una interfaz web en `/` para visualizar y manipular datos.

### 2. Task Worker (`cmd/worker/`)

Worker de Temporal que escucha la task queue `conciliation`. Registra:

- **`ConciliationWorkflow`**: El workflow principal (definido en `internal/workflows/conciliation.go`).
- **`GetPartnerStats`**: Activity que consulta la DB para evaluar el volumen de trabajo.
- **`LaunchAndWaitK8sJob`**: Activity que crea un K8s Job y espera su completion con heartbeats.

### 3. Conciliation Worker (`cmd/conciliation-worker/`)

Binario que corre **dentro del K8s Job** creado dinamicamente. Recibe su configuracion por env vars (`PARTNER_CODE`, `PARTNER_ID`, `TIER`, `DATABASE_URL`). Procesa las transacciones pendientes de un partner con concurrencia configurable por tier.

## Estructura del proyecto

```
.
├── cmd/
│   ├── trigger/                    # Trigger service (API + SQS + Backoffice)
│   │   ├── main.go                 # Entry point, SQS listener, API routes
│   │   ├── backoffice.go           # API handlers: partners, merchants, loans, transactions
│   │   └── backoffice_html.go      # HTML/JS embebido del backoffice
│   ├── worker/
│   │   └── main.go                 # Temporal worker, registra workflows y activities
│   └── conciliation-worker/
│       └── main.go                 # Procesador de conciliacion (corre en K8s Job)
├── internal/
│   ├── workflows/
│   │   └── conciliation.go         # ConciliationWorkflow: stats → resources → launch job
│   └── activities/
│       ├── db.go                    # GetPartnerStats: consulta metricas del partner
│       └── k8s.go                   # LaunchAndWaitK8sJob: crea Job, poll status
├── db/
│   └── schema.sql                  # Schema completo + seed data
├── deploy/
│   └── k8s/
│       ├── 00-namespace.yaml       # Namespace temporal-poc
│       ├── 01-postgres.yaml        # CloudNativePG cluster (3 DBs)
│       ├── 02-localstack.yaml      # LocalStack + setup SNS/SQS
│       ├── 03-temporal.yaml        # Temporal server + UI
│       ├── 04-seed-db.yaml         # Job para cargar schema + data
│       └── 05-app.yaml             # Deployments: task-worker, task-trigger + RBAC
├── Dockerfile.trigger
├── Dockerfile.worker
├── Dockerfile.conciliation-worker
├── go.mod                          # Module: github.com/your-org/task-server
└── go.sum
```

## Donde se guardan los datos

### Base de datos PostgreSQL (CloudNativePG)

Un solo cluster CNPG crea 3 bases de datos:

| Base de datos | Proposito | Accedida por |
|---|---|---|
| `conciliation` | Datos de negocio: partners, merchants, loans, transactions, collections | Trigger (reset, backoffice), Conciliation Worker (procesamiento) |
| `temporal` | Estado interno de Temporal: workflows, activities, task queues, timers | Temporal Server (exclusivo) |
| `temporal_visibility` | Indices de busqueda de workflows para Temporal UI | Temporal Server (exclusivo) |

**Connection string**: `postgres://temporal:temporal123@postgres-rw:5432/<db>?sslmode=disable`

Temporal guarda **todo su estado en PostgreSQL**: el historial de cada workflow execution, los inputs/outputs de cada activity, retries, timers, y el estado de las task queues. No usa almacenamiento local. Si el Temporal Server se reinicia, retoma todos los workflows pendientes desde la DB.

### Schema de negocio (`conciliation`)

```
partners ──1:N──► merchants ──1:1──► loans
                      │
                      └──1:N──► transactions ──1:1──► collections
```

- **`partners`**: Entidades que agrupan merchants (partner-alpha, partner-beta, etc.)
- **`merchants`**: Comercios asociados a un partner
- **`loans`**: Prestamos activos de cada merchant. Campos clave: `original_amount`, `remaining_amount`, `status` (active/paid)
- **`transactions`**: Transacciones de venta. Status: `pending` → `processed` | `skipped`
- **`collections`**: Registro de cada retencion aplicada (10% de la transaccion o el remaining del loan, lo menor)

### Temporal Server

Temporal no requiere almacenamiento propio mas alla de PostgreSQL. Todo queda en las tablas de `temporal` y `temporal_visibility`:

- **Workflow history**: Cada evento (activity started, completed, failed, timer fired) se persiste como un evento inmutable.
- **Task queues**: Se manejan en memoria pero se respaldan en DB para recovery.
- **Retry state**: Temporal sabe cuantos intentos lleva cada activity y cuando reintentar.

Puedes ver todo esto en la **Temporal UI** (`http://localhost:8233`):
- Lista de workflows con filtros
- Timeline de cada workflow (cada activity con su input/output)
- Stack traces en caso de error

## Flujo de una conciliacion

### Workflow (Temporal)

```
ConciliationWorkflow(partnerCode="partner-gamma", source="api")
│
├─ Activity: GetPartnerStats("partner-gamma")
│  └─ SQL: COUNT merchants, active loans, pending transactions
│  └─ Return: {PartnerID: 3, Merchants: 50, ActiveLoans: 35, PendingTxns: 25000}
│
├─ calculateResources(25000) → {CPU: "1", Memory: "1Gi", Tier: "medium"}
│
└─ Activity: LaunchAndWaitK8sJob(...)
   ├─ Crea K8s Job "conciliation-partner-gamma-1773082383"
   │  con requests/limits: cpu=1, memory=1Gi
   ├─ Poll cada 10s con heartbeat a Temporal
   └─ Return cuando job.Status.Succeeded > 0
```

### Procesamiento (dentro del K8s Job)

```
conciliation-worker started: partner=partner-gamma tier=medium concurrency=10
│
├─ Query: merchants con loans activos del partner
│  └─ 35 merchants encontrados
│
├─ Worker pool: 10 goroutines concurrentes
│  ├─ goroutine 1: processMerchant(merchant_id=51)
│  │   ├─ Query: transacciones pendientes del merchant
│  │   └─ Para cada transaccion (secuencial dentro del merchant):
│  │       ├─ BEGIN TX
│  │       ├─ SELECT remaining_amount FROM loans WHERE id=$1 FOR UPDATE
│  │       ├─ retention = min(amount * 10%, remaining)
│  │       ├─ INSERT INTO collections
│  │       ├─ UPDATE loans SET remaining_amount = remaining - retention
│  │       ├─ UPDATE transactions SET status = 'processed'
│  │       └─ COMMIT
│  ├─ goroutine 2: processMerchant(merchant_id=52)
│  ├─ ...
│  └─ goroutine 10: processMerchant(merchant_id=60)
│
└─ Conciliation completed: collections=13838 elapsed=42s
```

**Por que secuencial dentro de un merchant?** Porque todas las transacciones de un merchant afectan el mismo loan. El `SELECT FOR UPDATE` garantiza que no haya race conditions, pero ejecutarlas en paralelo causaria contention innecesaria en el mismo row lock.

**Por que paralelo entre merchants?** Porque cada merchant tiene su propio loan — no hay contention entre ellos.

## Concurrencia por tier

Definido en `cmd/conciliation-worker/main.go`:

| Tier | Concurrencia | DB Connections | CPU | RAM |
|------|-------------|---------------|-----|-----|
| light | 4 goroutines | 6 | 250m | 256Mi |
| medium | 10 goroutines | 12 | 1 | 1Gi |
| heavy | 20 goroutines | 22 | 2 | 4Gi |
| extra-heavy | 40 goroutines | 42 | 4 | 8Gi |

Los recursos del K8s Job se deciden en `internal/workflows/conciliation.go:calculateResources()` basado en la cantidad de transacciones pendientes.

La concurrencia del worker pool se decide en `cmd/conciliation-worker/main.go:concurrencyForTier()` basado en el tier asignado.

## Como disparar una conciliacion

### Opcion 1: Webhook directo

El API inicia el workflow en Temporal directamente.

```bash
curl -s -X POST http://localhost:8080/api/v1/conciliation \
  -d '{"partner_code": "partner-alpha"}'
```

Response:
```json
{"workflow_id": "conciliation-partner-alpha-1773078242016", "run_id": "...", "status": "started"}
```

### Opcion 2: SNS → SQS → Workflow

Se publica en el topic SNS. SNS entrega a la cola SQS suscrita. El trigger service consume de SQS e inicia el workflow.

```bash
AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1 \
  aws --endpoint-url=http://localhost:4566 sns publish \
  --topic-arn arn:aws:sns:us-east-1:000000000000:conciliation-topic \
  --message '{"partner_code": "partner-alpha"}'
```

El mensaje SNS se envuelve automaticamente en un envelope:
```json
{
  "Type": "Notification",
  "Message": "{\"partner_code\": \"partner-alpha\"}"
}
```
El trigger detecta si es SNS (envelope) o SQS directo.

### Reset de la base de datos

Vuelve transacciones a `pending`, loans a su monto original, y borra collections:

```bash
curl -s -X POST http://localhost:8080/api/v1/reset
```

## Backoffice UI

Disponible en `http://localhost:8080/`. Permite:

- Ver resumen de todos los partners (merchants, loans, transacciones, collections, monto recaudado)
- Click en un partner para ver detalle de merchants con estado de loans
- Editar el `remaining_amount` de un loan
- Crear transacciones en bulk para un merchant (ej: 1000 txns de $10,000)
- Resetear la base de datos
- Auto-refresh cada 10 segundos

### APIs del backoffice

| Metodo | Endpoint | Descripcion |
|--------|---------|-------------|
| GET | `/api/v1/partners` | Resumen de todos los partners |
| GET | `/api/v1/partners/{id}/merchants` | Merchants con detalle de loans y txns |
| PUT | `/api/v1/loans/{id}` | Modificar remaining amount de un loan |
| POST | `/api/v1/transactions/create` | Crear transacciones en bulk |
| POST | `/api/v1/reset` | Reset completo de la DB |
| POST | `/api/v1/conciliation` | Disparar workflow de conciliacion |

Ejemplo crear transacciones:
```bash
curl -s -X POST http://localhost:8080/api/v1/transactions/create \
  -d '{"merchant_id": 1, "count": 1000, "amount": 10000}'
```

## Como extender con un nuevo job

Para agregar un nuevo tipo de job (ej: un "settlement" o "reporting"), necesitas:

### 1. Crear el binario del worker

Crear `cmd/settlement-worker/main.go` con la logica de negocio. Este binario recibe su configuracion por env vars y se ejecuta dentro de un K8s Job.

```go
package main

func main() {
    partnerCode := os.Getenv("PARTNER_CODE")
    // ... tu logica
}
```

### 2. Crear su Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /settlement-worker ./cmd/settlement-worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /settlement-worker /settlement-worker
ENTRYPOINT ["/settlement-worker"]
```

### 3. Definir el workflow

Crear `internal/workflows/settlement.go`:

```go
package workflows

func SettlementWorkflow(ctx workflow.Context, input SettlementInput) (*SettlementResult, error) {
    // Step 1: Evaluar volumen (reusar GetPartnerStats o crear una activity nueva)
    var stats activities.PartnerStats
    err := workflow.ExecuteActivity(quickCtx, activities.GetPartnerStats, input.PartnerCode).Get(ctx, &stats)

    // Step 2: Calcular recursos
    resources := calculateSettlementResources(stats)

    // Step 3: Lanzar K8s Job (puedes reusar LaunchAndWaitK8sJob cambiando la imagen)
    jobInput := activities.LaunchJobInput{
        PartnerCode: input.PartnerCode,
        PartnerID:   stats.PartnerID,
        RecordCount: stats.PendingTransactions,
        Resources:   resources,
    }
    // Si necesitas una imagen distinta, agrega un campo ImageOverride a LaunchJobInput
    err = workflow.ExecuteActivity(jobCtx, activities.LaunchAndWaitK8sJob, jobInput).Get(ctx, &jobResult)

    return &SettlementResult{...}, nil
}
```

### 4. Registrar en el worker

En `cmd/worker/main.go`:

```go
w.RegisterWorkflow(workflows.SettlementWorkflow)
// Si creaste activities nuevas:
w.RegisterActivity(activities.MyNewActivity)
```

### 5. Agregar el trigger

En `cmd/trigger/main.go`, agregar un nuevo endpoint o reusar la cola SQS con un campo `type`:

```go
mux.HandleFunc("/api/v1/settlement", func(w http.ResponseWriter, r *http.Request) {
    // Iniciar SettlementWorkflow en vez de ConciliationWorkflow
    run, err := tc.ExecuteWorkflow(r.Context(), client.StartWorkflowOptions{
        ID:        fmt.Sprintf("settlement-%s-%d", input.PartnerCode, time.Now().UnixMilli()),
        TaskQueue: "conciliation", // misma queue, o crear una nueva
    }, "SettlementWorkflow", input)
})
```

### 6. Build, push, deploy

```bash
# Build
docker build --platform linux/amd64 -t localhost:5001/temporal-poc/settlement-worker:latest \
  -f Dockerfile.settlement-worker .

# Push
docker push localhost:5001/temporal-poc/settlement-worker:latest

# Rebuild worker y trigger si los modificaste
docker build --platform linux/amd64 -t localhost:5001/temporal-poc/worker:latest -f Dockerfile.worker .
docker push localhost:5001/temporal-poc/worker:latest

# Restart deployments
kubectl --context k3s -n temporal-poc rollout restart deployment/task-worker
kubectl --context k3s -n temporal-poc rollout restart deployment/task-trigger
```

### Puntos clave para extender

- **Reutiliza `LaunchAndWaitK8sJob`**: La activity ya sabe crear Jobs, poll status, y reportar heartbeats. Solo necesitas pasarle inputs distintos. Si necesitas otra imagen, agrega un campo `Image` a `LaunchJobInput`.
- **Task Queue**: Puedes usar la misma (`conciliation`) o crear task queues separadas si quieres workers dedicados.
- **RBAC ya esta**: El ServiceAccount `task-server` tiene permisos para crear/listar/borrar Jobs en el namespace.
- **Temporal maneja retries**: Si tu Job falla, Temporal reintenta segun el `RetryPolicy` configurado en el workflow.

## Deployment

### Prerequisitos

- K3s cluster (context: `k3s`)
- CloudNativePG operator instalado
- Registry accesible (en este PoC: `registry.csepulveda.net` via nginx ingress, o port-forward a `localhost:5001`)

### Desplegar todo desde cero

```bash
# 1. Aplicar manifiestos en orden
kubectl --context k3s apply -f deploy/k8s/00-namespace.yaml
kubectl --context k3s apply -f deploy/k8s/01-postgres.yaml
# Esperar que el cluster CNPG este ready
kubectl --context k3s -n temporal-poc wait --for=condition=Ready cluster/postgres --timeout=120s

kubectl --context k3s apply -f deploy/k8s/02-localstack.yaml
kubectl --context k3s apply -f deploy/k8s/03-temporal.yaml
kubectl --context k3s apply -f deploy/k8s/04-seed-db.yaml
kubectl --context k3s apply -f deploy/k8s/05-app.yaml

# 2. Build imagenes (desde macOS arm64 para k3s amd64)
docker build --platform linux/amd64 -t localhost:5001/temporal-poc/trigger:latest -f Dockerfile.trigger .
docker build --platform linux/amd64 -t localhost:5001/temporal-poc/worker:latest -f Dockerfile.worker .
docker build --platform linux/amd64 -t localhost:5001/temporal-poc/conciliation-worker:latest -f Dockerfile.conciliation-worker .

# 3. Push
docker push localhost:5001/temporal-poc/trigger:latest
docker push localhost:5001/temporal-poc/worker:latest
docker push localhost:5001/temporal-poc/conciliation-worker:latest

# 4. Restart deployments para tomar imagenes nuevas
kubectl --context k3s -n temporal-poc rollout restart deployment/task-worker
kubectl --context k3s -n temporal-poc rollout restart deployment/task-trigger
```

### Port-forwards (desarrollo local)

```bash
kubectl --context k3s -n temporal-poc port-forward svc/task-trigger 8080:8080 &   # API + Backoffice
kubectl --context k3s -n temporal-poc port-forward svc/temporal-ui 8233:8080 &    # Temporal UI
kubectl --context k3s -n temporal-poc port-forward svc/localstack 4566:4566 &     # LocalStack
kubectl --context k3s -n registry port-forward svc/registry 5001:5000 &           # Registry
```

## Monitoreo

```bash
# Temporal UI — ver workflows, timeline, inputs/outputs de cada activity
open http://localhost:8233

# Backoffice — ver estado de partners, merchants, loans en tiempo real
open http://localhost:8080

# Ver K8s Jobs activos y sus recursos
kubectl --context k3s -n temporal-poc get jobs -l managed-by=task-server

# Logs del conciliation worker (el Job)
kubectl --context k3s -n temporal-poc logs -l app=conciliation-worker -f

# Logs del task worker (orquestacion Temporal)
kubectl --context k3s -n temporal-poc logs -l app=task-worker -f

# Logs del trigger (SQS listener + API)
kubectl --context k3s -n temporal-poc logs -l app=task-trigger -f
```

## Stack

| Componente | Tecnologia | Proposito |
|---|---|---|
| Orquestacion | Temporal (open source, self-hosted) | Workflows, retries, durabilidad |
| Lenguaje | Go 1.22 | Trigger, worker, conciliation worker |
| Base de datos | PostgreSQL 16 (CloudNativePG) | Datos de negocio + estado de Temporal |
| Mensajeria | LocalStack (SNS/SQS) | Eventos async en dev local |
| Kubernetes | K3s | Cluster local para Jobs dinamicos |
| Registry | In-cluster registry | Imagenes Docker |
