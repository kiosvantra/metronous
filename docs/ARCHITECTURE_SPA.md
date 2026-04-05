> Esta es la traducción al español de ARCHITECTURE.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Arquitectura

Este documento es la inmersión técnica profunda en los aspectos internos de Metronous: límites de componentes, flujos de datos, la máquina de estado del plugin, el pipeline de benchmark y las reglas del motor de decisiones.

Para la metodología de benchmarks y fórmulas de puntuación consulta [BENCHMARKS.md](BENCHMARKS.md).

---

## Tabla de Contenidos

- [Panorama de componentes](#panorama-de-componentes)
- [Diagrama de componentes](#diagrama-de-componentes)
- [Protocolos de comunicación](#protocolos-de-comunicación)
- [Flujo de datos: Ingestión de eventos](#flujo-de-datos-ingestión-de-eventos)
- [Flujo de datos: Pipeline de benchmark](#flujo-de-datos-pipeline-de-benchmark)
- [Máquina de estado del plugin](#máquina-de-estado-del-plugin)
- [Plugin: Seguimiento de costos](#plugin-seguimiento-de-costos)
- [Diseño de pestañas del TUI](#diseño-de-pestañas-del-tui)
- [Ciclo de vida del daemon](#ciclo-de-vida-del-daemon)
- [Scheduler](#scheduler)
- [Esquema SQLite (simplificado)](#esquema-sqlite-simplificado)
- [Estructura de directorios](#estructura-de-directorios)
- [Manejo de fallos](#manejo-de-fallos)
- [Extensibilidad](#extensibilidad)

---

## Panorama de componentes

| Componente | Lenguaje | Responsabilidad |
|------------|----------|-----------------|
| `metronous-plugin.ts` | TypeScript | Plugin de OpenCode; captura eventos de agentes y los reenvía al daemon via HTTP POST |
| `metronous mcp` (shim) | Go | Puente stdio↔HTTP iniciado por OpenCode como servidor MCP; traduce mensajes MCP a llamadas HTTP |
| `metronous server --daemon-mode` | Go | Daemon de fondo de larga duración (systemd/launchd/SCM); ingesta eventos, los almacena en SQLite, ejecuta benchmarks semanales |
| `internal/tracking/` | Go | Manejador de ingestión HTTP + cola de escritura asíncrona |
| `internal/store/sqlite/` | Go | Implementaciones SQLite de `EventStore` y `BenchmarkStore` |
| `internal/benchmark/` | Go | Cálculo de métricas a partir de eventos sin procesar (`AggregateMetrics`) |
| `internal/decision/` | Go | Evaluación de umbrales y asignación de veredictos |
| `internal/runner/` | Go | Orquesta el pipeline de benchmark (semanal e intra-semana) |
| `internal/scheduler/` | Go | Scheduler de trabajo semanal basado en cron integrado en el daemon |
| `internal/tui/` | Go | Interfaz de terminal de 5 pestañas con Bubble Tea |
| `cmd/metronous/` | Go | Punto de entrada de la CLI (comandos Cobra: install, dashboard, service, …) |

---

## Diagrama de componentes

```
┌─────────────────────────────────────────────────────────────────────┐
│  OpenCode process                                                     │
│                                                                       │
│  ┌──────────────────────────┐   HTTP POST /ingest                    │
│  │  metronous-plugin.ts     │──────────────────────────────────┐     │
│  │  (OpenCode plugin)       │                                  │     │
│  └──────────────────────────┘                                  │     │
│                                                                 │     │
│  ┌──────────────────────────┐   MCP stdio   ┌──────────────┐  │     │
│  │  OpenCode (MCP client)   │◄─────────────►│ metronous mcp│  │     │
│  └──────────────────────────┘               │ (shim)       │──┘     │
│                                             └──────────────┘         │
└─────────────────────────────────────────────────────────────────────┘
                                       │ HTTP POST /ingest
                                       ▼
                          ┌────────────────────────┐
                          │  metronous daemon       │
                          │  (systemd user service) │
                          │                         │
                          │  ┌───────────────────┐  │
                          │  │ tracking.IngestHandler│
                          │  │ → EventQueue        │  │
                          │  └────────┬──────────┘  │
                          │           │ async write  │
                          │  ┌────────▼──────────┐  │
                          │  │  tracking.db      │  │
                          │  │  (SQLite WAL)     │  │
                          │  └───────────────────┘  │
                          │                         │
                          │  ┌───────────────────┐  │
                          │  │  benchmark.db     │  │
                          │  │  (SQLite WAL)     │  │
                          │  └─────────▲─────────┘  │
                          │            │             │
                          │  ┌─────────┴─────────┐  │
                          │  │  Scheduler (cron)  │  │
                          │  │  Monday 02:00      │  │
                          │  │  Runner → Engine   │  │
                          │  └───────────────────┘  │
                          └────────────────────────┘
                                       │
                          ┌────────────▼────────────┐
                          │  metronous dashboard     │
                          │  (TUI, reads SQLite)     │
                          └─────────────────────────┘
```

---

## Protocolos de comunicación

### Plugin → Daemon (HTTP)

El plugin envía eventos directamente al daemon via HTTP POST, omitiendo completamente el shim MCP. El puerto del daemon se lee desde `~/.metronous/data/mcp.port`.

- **Endpoint**: `POST http://127.0.0.1:<port>/ingest`
- **Cuerpo**: payload JSON con `agent_id`, `session_id`, `event_type`, `model`, `timestamp` y campos opcionales de costo/token
- **Reconexión**: ante `ECONNREFUSED`, el plugin vuelve a leer `mcp.port` y reintenta hasta 3 veces con un backoff de 500ms
- **Cola previa a estar listo**: los eventos emitidos antes de que el daemon esté listo se almacenan en un buffer (máx. 500) y se vacían una vez que aparece el archivo de puerto

### Shim → Daemon (HTTP, para llamadas de origen MCP)

El shim `metronous mcp` traduce mensajes MCP `tools/call ingest` de OpenCode en solicitudes HTTP POST al mismo endpoint `/ingest`.

### TUI / CLI → SQLite (directo)

El TUI y la CLI leen ambos archivos SQLite directamente (sin capa HTTP). El daemon mantiene los archivos abiertos en modo WAL, por lo que las lecturas concurrentes desde el TUI son seguras.

---

## Flujo de datos: Ingestión de eventos

```
1. OpenCode lanza un evento (chat.message, tool.execute.after, event hook)
2. metronous-plugin.ts construye un payload JSON
3. callIngest() → httpPost() → POST http://127.0.0.1:<port>/ingest
4. Manejador HTTP del daemon → tracking.HandleIngest()
5. validateIngestRequest() valida los campos requeridos (agent_id, session_id, event_type, model, timestamp)
6. toStoreEvent() convierte a store.Event
7. EventQueue.Enqueue() almacena el evento en buffer para escritura asíncrona
8. Goroutine de fondo de EventQueue → EventStore.InsertEvent() → tracking.db
```

**Tipos de evento válidos**: `start`, `tool_call`, `retry`, `complete`, `error`

---

## Flujo de datos: Pipeline de benchmark

```
Disparador (cron semanal o F5 intra-semana desde la pestaña Benchmark Detailed)
    │
    ▼
Runner.run()
    │
    ├── discoverAgents() ─── QueryEvents() filtrado a eventos que no son error
    │
    ├── readActiveModelFromConfig() ─── lee ~/.config/opencode/opencode.json
    │       determina el modelo actualmente configurado por agente
    │
    └── para cada agentID:
            │
            ├── FetchEventsForWindow() ─── todos los eventos en [start, end)
            │
            ├── agrupa por nombre de modelo normalizado
            │
            └── para cada (agentID, model):
                    │
                    ├── AggregateMetrics()
                    │       Accuracy, ErrorRate, AvgTurnMs, P50/P95/P99,
                    │       PromptTokens, CompletionTokens, TotalCostUSD,
                    │       SessionCount, ROIScore
                    │
                    ├── assignRunStatus()
                    │       model == activeModel → run_status = 'active'
                    │       de lo contrario    → run_status = 'superseded'
                    │       (fallback: mayor conteo de eventos si el agente no está en la config)
                    │
                    ├── DecisionEngine.Evaluate()
                    │       EvaluateRulesWithPricing() → VerdictType
                    │       BuildReasonWithPricing() → cadena de razón legible
                    │       recommendModel() → reemplazo sugerido
                    │
                    └── bestAlternativeModel()
                            selecciona un mejor modelo de los datos de la ventana actual
                            (accuracy primero, luego ROI, luego avg turn time)

Después de todos los agentes:
    ├── MarkSupersededRuns() → actualiza filas 'active' previas a 'superseded'
    │       cuando el modelo activo ha cambiado desde el ciclo anterior
    ├── GenerateArtifact() → ~/.metronous/artifacts/<timestamp>.json
    └── BenchmarkStore.SaveRun() → benchmark.db (una fila por par agentID/model)
```

---

## Máquina de estado del plugin

El plugin mantiene un `sessions` Map desde `sessionID → SessionState`. Las sesiones se crean de forma diferida en el primer evento y se retienen indefinidamente (el mapa se resetea al reiniciar OpenCode).

```
Evento recibido
    │
    ├── chat.message / chat.params
    │       ├── Resolver agentId (METRONOUS_AGENT_ID > chat.agent > "opencode")
    │       ├── Construir cadena del modelo ("providerID/modelID")
    │       ├── getOrCreateSession() — restaura el costo de session_costs.json si es nuevo
    │       ├── Actualizar state.model si actualmente es "unknown" y el nuevo modelo tiene "/"
    │       ├── Actualizar state.lastActiveModel solo si el nuevo modelo contiene "/" (prefijo de proveedor)
    │       └── Si sesión nueva → callIngest({ event_type: "start" })
    │
    ├── tool.execute.after
    │       ├── Incrementar toolCalls / successfulToolCalls / errors
    │       ├── Detección de reelaboración: misma herramienta en 60s → reworkCount++
    │       └── callIngest({ event_type: "tool_call", cost_usd: state.totalCostUsd })
    │           (cost_usd aquí es una instantánea retrasada del último step-finish)
    │
    └── event hook
            ├── message.part.updated (step-finish)
            │       ├── state.totalCostUsd += max(0, part.cost)  ← costo por llamada
            │       ├── state.promptTokens += part.tokens.input
            │       ├── state.completionTokens += part.tokens.output
            │       └── saveCostCache(sessionId, totalCostUsd) → session_costs.json
            │
            ├── session.idle
            │       ├── Instantánea durationMs = Date.now() - startTime
            │       ├── saveCostCache()
            │       ├── calculateQualityProxy() (penaliza fallos, reelaboraciones, errores)
            │       ├── callIngest({ event_type: "complete", cost_usd: totalCostUsd })
            │       └── state.lastIdleAt = now (se mantiene en memoria, no se expulsa)
            │
            └── session.error
                    ├── state.errors++
                    └── callIngest({ event_type: "error" })
```

### Campos inactivos en SessionState

`completedSegmentsCost` y `lastStepCost` se retienen por compatibilidad de estructura pero nunca se escriben ni se leen en el código activo. El costo se rastrea exclusivamente via `totalCostUsd` (acumulado desde los deltas de step-finish).

---

## Plugin: Seguimiento de costos

El plugin calcula el costo de sesión acumulando el campo `cost` de los parts `step-finish` entregados via `message.part.updated`:

```typescript
state.totalCostUsd += Math.max(0, part.cost)  // costo por llamada LLM, no acumulativo
```

Esto coincide con la facturación del proveedor porque cada `step-finish` reporta el costo de una solicitud LLM.

**Cache de costo** (`session_costs.json`):
- Se carga al inicio del plugin; restaura `totalCostUsd` para sesiones activas antes de un reinicio
- Se escribe de forma síncrona en cada `step-finish` y en `session.idle`
- Los valores no finitos se rechazan tanto en la lectura como en la escritura

**Regla de selección de modelo**: `lastActiveModel` solo se actualiza cuando la cadena del modelo contiene `/` (prefijo de proveedor). Esto evita que nombres de modelo sin prefijo como `claude-sonnet-4-6` sobreescriban un `opencode/claude-sonnet-4-6` completamente cualificado ya registrado.

---

## Diseño de pestañas del TUI

El TUI usa Bubble Tea y está compuesto por cinco sub-modelos renderizados como pestañas numeradas:

| # | Nombre de la pestaña | Modelo | Archivo |
|---|---------------------|--------|---------|
| 1 | **Benchmark History Summary** | `BenchmarkSummaryModel` | `benchmark_summary_view.go` |
| 2 | **Benchmark Detailed** | `BenchmarkDetailedModel` | `benchmark_view.go` |
| 3 | **Tracking** | `TrackingModel` | `tracking_view.go` |
| 4 | **Charts** | `ChartsModel` | `charts_view.go` |
| 5 | **Config** | `ConfigModel` | `config_view.go` |

El cambio de pestañas es manejado por `app.go`. Todas las pestañas excepto Config se actualizan en un tick de 2 segundos desde las stores SQLite.

**Propagación de recarga de configuración**: cuando la pestaña Config guarda los umbrales, emite `ConfigReloadedMsg`. Tanto `BenchmarkSummaryModel` como `ChartsModel` se suscriben a este mensaje y actualizan su campo `minROI` para que los puntajes de salud y responsabilidad se recalculen con el nuevo umbral de inmediato.

---

## Ciclo de vida del daemon

```
metronous server --daemon-mode
    │
    ├── os.MkdirAll(DataDir)
    ├── sqlite.NewEventStore(tracking.db)  ← modo WAL
    ├── sqlite.NewBenchmarkStore(benchmark.db)  ← modo WAL
    ├── loadThresholds() desde ~/.metronous/thresholds.json (predeterminados en caso de error)
    ├── decision.NewDecisionEngine(&thresholds)
    ├── runner.NewRunner(es, bs, engine, artifactDir)
    ├── scheduler.NewSchedulerWithContext(ctx, runner, 7, logger)
    │       └── RegisterWeeklyJob("0 0 2 * * 1")  ← lunes 02:00 hora local
    ├── tracking.NewEventQueue(es, DefaultBufferSize)  ← buffer de escritura asíncrona
    ├── mcp.NewStdioServer()  ← también inicia el servidor HTTP en puerto dinámico
    │       └── escribe el puerto en mcp.port
    └── srv.ServeDaemon(ctx)  ← bloquea hasta que ctx sea cancelado

Al apagarse (SIGTERM / detención del servicio):
    └── ctx cancelado → scheduler se detiene → cola se vacía → WAL checkpoint → bases de datos se cierran
```

El daemon se ejecuta como un **servicio de usuario systemd** en Linux:
- Archivo de unidad: `~/.config/systemd/user/metronous.service`
- Habilitado con alcance `--user`; inicia al iniciar sesión, se mantiene activo con lingering
- Política de reinicio: `Restart=on-failure`, `RestartSec=5s`
- Logs: `~/.metronous/data/metronous.log` + `journalctl --user -u metronous`

---

## Scheduler

El scheduler de benchmark semanal (`internal/scheduler/cron.go`) está integrado directamente en el proceso del daemon usando `robfig/cron/v3` con análisis de precisión en segundos.

- **Horario**: `"0 0 2 * * 1"` — lunes a las 02:00 hora local
- **Ventana**: últimos 7 días (`DefaultWindowDays = 7`)
- **Intra-semana (F5)**: `Runner.RunIntraweek()` deriva el inicio de la ventana desde `max(run_at)` de todas las ejecuciones de benchmark anteriores, por lo que solo se evalúan los eventos desde la última ejecución
- **Cancelación**: el contexto del scheduler se deriva del contexto del daemon; los trabajos en progreso observan la cancelación al apagarse el daemon

---

## Esquema SQLite (simplificado)

### tracking.db — tabla de eventos

```sql
id               TEXT PRIMARY KEY,
agent_id         TEXT NOT NULL,
session_id       TEXT NOT NULL,
event_type       TEXT NOT NULL,  -- start | tool_call | retry | complete | error
model            TEXT NOT NULL,
timestamp        DATETIME NOT NULL,
duration_ms      INTEGER,         -- solo en eventos complete; tool_call siempre 0
prompt_tokens    INTEGER,         -- solo en eventos complete
completion_tokens INTEGER,        -- solo en eventos complete
cost_usd         REAL,            -- acumulativo por sesión en el momento del evento
quality_score    REAL,
rework_count     INTEGER,
tool_name        TEXT,
tool_success     BOOLEAN,
metadata         JSON
```

### benchmark.db — tabla benchmark_runs

```sql
run_at            DATETIME NOT NULL,
agent_id          TEXT NOT NULL,
model             TEXT NOT NULL,   -- nombre de modelo normalizado (prefijo de proveedor eliminado)
raw_model         TEXT,            -- nombre completo con prefijo de proveedor (p. ej. opencode/claude-sonnet-4-6)
run_kind          TEXT,            -- weekly | intraweek
run_status        TEXT,            -- active | superseded
window_days       INTEGER,
window_start      DATETIME,
window_end        DATETIME,
accuracy          REAL,
avg_latency_ms    REAL,            -- alias obsoleto de avg_turn_ms
p50_latency_ms    REAL,
p95_latency_ms    REAL,
p99_latency_ms    REAL,
avg_turn_ms       REAL,            -- duración de turno media (solo eventos complete)
p95_turn_ms       REAL,
tool_success_rate REAL,            -- siempre 1.0 en la práctica
roi_score         REAL,
total_cost_usd    REAL,
sample_size       INTEGER,
verdict           TEXT,            -- KEEP | SWITCH | URGENT_SWITCH | INSUFFICIENT_DATA
recommended_model TEXT,
decision_reason   TEXT,
artifact_path     TEXT
```

---

## Estructura de directorios

```
~/.metronous/
├── data/
│   ├── tracking.db          # Flujo de eventos sin procesar (modo WAL)
│   ├── benchmark.db         # Historial de ejecuciones de benchmark (modo WAL)
│   ├── mcp.port             # Puerto HTTP dinámico (tiempo de ejecución)
│   ├── metronous.pid        # PID del daemon (tiempo de ejecución)
│   ├── session_costs.json   # Cache de costo del plugin (sobrevive reinicios)
│   └── plugin.log           # Log de depuración del plugin (METRONOUS_DEBUG=true)
├── artifacts/
│   └── <timestamp>.json     # Artefacto de decisión por ejecución de benchmark
└── thresholds.json          # Configuración de umbrales editable por el usuario
```

---

## Manejo de fallos

| Fallo | Detección | Mitigación |
|-------|-----------|------------|
| Archivo de puerto ausente al inicio del plugin | `readPortFile()` retorna null | `waitForServer()` sondea durante 30s; eventos almacenados en `preReadyQueue` (máx. 500) |
| Daemon reiniciado en mitad de sesión | `ECONNREFUSED` en HTTP POST | El plugin vuelve a leer `mcp.port` y reintenta hasta 3 veces con backoff de 500ms |
| Reinicio del plugin en mitad de sesión | Sesión no en memoria | `getOrCreateSession()` restaura el costo desde `session_costs.json` |
| Crash del daemon | systemd `Restart=on-failure` | El daemon se reinicia en 5s; el plugin se reconecta en el siguiente evento |
| Disco lleno en SQLite | Store retorna error al insertar | La cola registra el error; el evento se descarta; el daemon continúa |
| Costo no finito en step-finish | Verificación `Number.isFinite()` | Silenciosamente ajustado a 0; error registrado en `plugin.log` |
| Todos los eventos son errores para un agente | `discoverAgents()` filtra agentes solo-error | El agente se excluye del benchmark; sin fila `INSUFFICIENT_DATA` engañosa |

---

## Extensibilidad

### Agregar una nueva métrica rastreada

1. Agregar una columna a la tabla de eventos de `tracking.db` en `internal/store/sqlite/event_store.go`
2. Agregar el campo a la estructura `store.Event` en `internal/store/store.go`
3. Emitir el campo desde el plugin (`metronous-plugin.ts`)
4. Manejarlo en `tracking.validateIngestRequest()` y `toStoreEvent()`
5. Consumirlo en `benchmark.AggregateMetrics()` y exponerlo via `WindowMetrics`

### Cambiar el backend de almacenamiento

Reemplazar `internal/store/sqlite/` con una implementación de las interfaces `store.EventStore` y `store.BenchmarkStore`. El TUI y la CLI también leen SQLite directamente; esas rutas necesitarían consultar via HTTP o una nueva interfaz.

### Agregar una nueva pestaña al TUI

1. Crear un nuevo sub-modelo Bubble Tea que implemente `Init()`, `Update()`, `View()`
2. Registrarlo en `internal/tui/app.go` junto con la lógica de cambio de pestaña existente
3. Conectarlo a la store correspondiente via `NewXxxModel(es, bs)`
