> Esta es la traducción al español de BENCHMARKS.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Metodología de Benchmarks

Este documento describe exactamente cómo Metronous calcula las métricas, asigna veredictos y computa las puntuaciones compuestas mostradas en el TUI. Todo lo aquí descrito se deriva directamente del código fuente.

---

## Tabla de Contenidos

- [Tamaño mínimo de muestra](#tamaño-mínimo-de-muestra)
- [Cálculo de métricas](#cálculo-de-métricas)
  - [Accuracy](#accuracy)
  - [Turn Latency](#turn-latency)
  - [Token Counts](#token-counts)
  - [Cost Per Session](#cost-per-session)
  - [ROI Score](#roi-score)
- [Lógica de veredictos](#lógica-de-veredictos)
  - [INSUFFICIENT_DATA](#insufficient_data)
  - [URGENT_SWITCH](#urgent_switch)
  - [SWITCH](#switch)
  - [KEEP](#keep)
  - [Reglas de supresión de ROI](#reglas-de-supresión-de-roi)
  - [Recomendación de modelo](#recomendación-de-modelo)
- [Health Score](#health-score)
- [Responsibility Score](#responsibility-score)
- [Umbrales de configuración](#umbrales-de-configuración)
  - [Campos activos (pestaña Config del TUI)](#campos-activos-pestaña-config-del-tui)
  - [Disparadores urgentes](#disparadores-urgentes)
  - [Sobreescrituras por agente](#sobreescrituras-por-agente)
  - [Precios de modelos](#precios-de-modelos)
- [Tipos de ejecución de benchmark](#tipos-de-ejecución-de-benchmark)
- [Determinación del modelo activo](#determinación-del-modelo-activo)
- [Estado de ejecución](#estado-de-ejecución)
- [Evaluación por modelo](#evaluación-por-modelo)
- [Selección del mejor modelo alternativo](#selección-del-mejor-modelo-alternativo)
- [Tendencia de veredicto](#tendencia-de-veredicto)
- [Visualización de tiempo](#visualización-de-tiempo)
- [Campo Raw Model](#campo-raw-model)
- [Campos obsoletos](#campos-obsoletos)

---

## Tamaño mínimo de muestra

```go
const MinSampleSize = 50  // internal/benchmark/metrics.go
```

Cualquier par agente/modelo con menos de 50 eventos en la ventana de evaluación recibe `INSUFFICIENT_DATA`. Todos los cálculos de métricas se realizan igualmente; el veredicto se fuerza independientemente de los valores reales.

---

## Cálculo de métricas

Todas las métricas son calculadas por `AggregateMetrics()` en `internal/benchmark/fetcher.go`.

### Accuracy

```
Accuracy = (total_events - error_events) / total_events
```

- `total_events` = todos los eventos en la ventana (de cualquier tipo)
- `error_events` = eventos con `event_type == "error"`
- Rango: [0.0, 1.0]. Retorna 0 si total_events es 0.

```go
// internal/benchmark/metrics.go
func CalculateAccuracy(completed, total int) float64 {
    if total == 0 { return 0 }
    return float64(completed) / float64(total)
}
```

### Turn Latency

La latencia de turno se deriva **exclusivamente de eventos `complete`** con `duration_ms > 0`.

- Los eventos `tool_call` siempre tienen `duration_ms == 0` y se excluyen.
- `AvgTurnMs` = media aritmética de todas las duraciones de eventos complete
- `P50TurnMs`, `P95TurnMs`, `P99TurnMs` = percentiles de rango más cercano (índice basado en piso)

```go
// internal/benchmark/metrics.go — fórmula del índice de percentil
idx := rank * n / 100
if idx > 0 && rank*n%100 == 0 { idx-- }
```

Nota: `duration_ms` en eventos `complete` es el tiempo de reloj de pared desde el inicio de la sesión hasta el evento de completado, no una latencia LLM por llamada. Es útil como comparación relativa, pero no debe interpretarse como latencia estricta por solicitud.

### Token Counts

`AvgPromptTokens` y `AvgCompletionTokens` son medias calculadas solo sobre eventos complete:

```
AvgPromptTokens = sum(prompt_tokens en eventos complete) / count(eventos complete con tokens)
```

### Cost Per Session

El costo se registra como el **valor máximo de `cost_usd` por `session_id` distinto**, luego se suma a través de todas las sesiones en la ventana:

```
TotalCostUSD = suma sobre sesiones de MAX(cost_usd) por sesión
```

Esto es correcto porque el plugin emite `cost_usd` como el costo de sesión **acumulado** en el momento de cada evento. Tomar el máximo por sesión recupera el costo final sin contar doble las instantáneas intermedias.

```
CostPerSession = TotalCostUSD / SessionCount
```

Los eventos sin `session_id` y con costo no nulo se descartan con una advertencia (no pueden atribuirse a una sesión).

### ROI Score

```
ROI = Accuracy / CostPerSession
```

- Mide la salida precisa por dólar gastado
- Retorna 0 cuando `CostPerSession == 0` (no hay datos de facturación disponibles)
- La fórmula anterior usaba `ToolSuccessRate / CostPerSession`, pero `ToolSuccessRate` siempre es 1.0 en la práctica, por lo que fue reemplazada por `Accuracy` que porta señal real

```go
// internal/benchmark/fetcher.go
if costPerSession > 0 {
    m.ROIScore = m.Accuracy / costPerSession
}
```

---

## Lógica de veredictos

Los veredictos son asignados por `EvaluateRulesWithPricing()` en `internal/decision/verdict.go`. Las reglas se verifican en este orden exacto:

### INSUFFICIENT_DATA

```
if SampleSize < 50 → INSUFFICIENT_DATA
```

Se verifica primero. Ninguna otra regla se activa.

### URGENT_SWITCH

```
if Accuracy < urgent.MinAccuracy (default 0.60) → URGENT_SWITCH
if ErrorRate > urgent.MaxErrorRate (default 0.30) → URGENT_SWITCH
```

Los disparadores urgentes se verifican antes que los umbrales suaves. Cualquier condición urgente activa `URGENT_SWITCH`.

### SWITCH

```
if Accuracy < thresholds.MinAccuracy (default 0.85) → SWITCH
if ROI_active AND ROIScore < thresholds.MinROIScore (default 0.05) → SWITCH
```

La latencia está intencionalmente excluida de los disparadores SWITCH. El campo `duration_ms` refleja el tiempo de sesión acumulativo, no la latencia por llamada, y es demasiado ruidoso para usarse como disparador de umbral.

### KEEP

```
si ninguna de las condiciones anteriores se activó → KEEP
```

### Reglas de supresión de ROI

El ROI se excluye de la evaluación de decisiones cuando se cumple alguna de estas condiciones:

1. **Modelo gratuito**: el modelo está listado en `model_pricing` con `price == 0`
2. **Datos de costo no confiables**: `TotalCostUSD == 0` (no se recopilaron eventos de facturación)

```go
// internal/decision/verdict.go
func roiActive(model string, m benchmark.WindowMetrics, thresholds *config.Thresholds) bool {
    if thresholds.IsModelFree(model) { return false }
    if m.TotalCostUSD == 0 { return false }
    return true
}
```

Cuando el ROI se suprime, la cadena de razón muestra `roi=N/A (free model)` o `roi=N/A (no billing data)`.

### Recomendación de modelo

Cuando el veredicto es `SWITCH` o `URGENT_SWITCH`, el motor primero intenta `bestAlternativeModel()` para encontrar un mejor modelo a partir de **datos reales de benchmark en la misma ventana**. Si no se encuentra ninguno, recurre a recomendaciones basadas en la configuración:

- **Falla de accuracy** → `model_recommendations.accuracy_model` (predeterminado: `claude-opus-4-5`)
- **Falla de ROI** → `model_recommendations.performance_model` (predeterminado: `claude-haiku-4-5`)
- **Fallback** → `model_recommendations.default_model` (predeterminado: `claude-sonnet-4-5`)

---

## Health Score

El health score es un valor compuesto de 0–100 mostrado en la pestaña Benchmark History Summary. Combina tres señales:

```
HealthScore = AccuracyPart + VerdictPart + ROIPart
```

| Componente | Peso | Fórmula |
|------------|------|---------|
| **Accuracy** | 60 pts | `accuracy * 60` |
| **Verdict** | 0–25 pts | KEEP=25, INSUFFICIENT_DATA=10, SWITCH=5, URGENT_SWITCH=0 |
| **ROI** | 0–15 pts | `15 * min(1, roiScore / minROIScore)` — 7 pts neutros cuando no hay datos de costo |

```go
// internal/tui/benchmark_summary_view.go
func computeHealthScore(accuracy, _ float64, verdict store.VerdictType, roiScore, minROIScore float64) float64 {
    accPart     := accuracy * 60
    verdictPart := // ver tabla arriba
    roiPart     := // 7 neutro, o 15 * min(1, roi/minROI)
    return clamp(accPart + verdictPart + roiPart, 0, 100)
}
```

La latencia está **excluida** del health score por la misma razón por la que se excluye de los disparadores SWITCH: `p95_latency_ms` actualmente refleja el tiempo de sesión acumulativo, no la latencia por llamada.

**Codificación de colores**:
- `>= 80` → verde
- `>= 50` → amarillo
- `< 50`  → rojo

---

## Responsibility Score

El Responsibility Score aparece en la tarjeta "Responsibility Top 3" de la pestaña Charts. Mide la contribución de salud de un modelo ponderada por la **importancia empresarial de los agentes** que lo usan.

```
ResponsibilityScore = sum(HealthScore(run) * agentWeight(run.AgentID) * run.SampleSize)
                    / sum(run.SampleSize)
```

Los pesos de los agentes están definidos en `internal/tui/charts_view.go`:

| Agente | Peso |
|--------|------|
| `sdd-orchestrator` | 1.00 |
| `sdd-apply` | 0.98 |
| `sdd-verify` | 0.96 |
| `sdd-explore` | 0.94 |
| `sdd-design` | 0.92 |
| `sdd-spec` | 0.90 |
| `sdd-propose` | 0.88 |
| `sdd-tasks` | 0.87 |
| `sdd-archive` | 0.86 |
| `sdd-init` | 0.85 |
| Otros `sdd-*` | 0.90 |
| `build`, `plan`, `general`, `explore` | 0.80 |
| Todos los demás | 0.75 |

Un modelo con alta salud en `sdd-orchestrator` y `sdd-apply` (agentes de mayor peso) obtiene una puntuación más alta que uno con salud idéntica concentrada en agentes de archivo.

Cuando `roleWeightSum == 0` (no hay ejecuciones de benchmark con datos suficientes), `ResponsibilityScore = HealthScore * 0.75`.

---

## Umbrales de configuración

Los umbrales se almacenan en `~/.metronous/thresholds.json` y son cargados por el daemon al inicio.

### Campos activos (pestaña Config del TUI)

Estos tres campos son editables via la pestaña Config (`5`):

| Campo | Clave JSON | Predeterminado | Descripción |
|-------|------------|----------------|-------------|
| **Min Accuracy** | `defaults.min_accuracy` | `0.85` | Accuracy por debajo de este valor → `SWITCH` |
| **Min ROI Score** | `defaults.min_roi_score` | `0.05` | ROI por debajo de este valor → `SWITCH` (solo modelos de pago) |
| **Max Cost/Session** | `defaults.max_cost_usd_per_session` | `0.50` | Referencia para el color del semáforo de costo en la pestaña Tracking; base de detección de picos |

### Disparadores urgentes

Estos no están expuestos en la pestaña Config, pero están presentes en `thresholds.json`:

| Campo | Clave JSON | Predeterminado | Descripción |
|-------|------------|----------------|-------------|
| Urgente min accuracy | `urgent_triggers.min_accuracy` | `0.60` | Por debajo de este valor → `URGENT_SWITCH` |
| Max error rate | `urgent_triggers.max_error_rate` | `0.30` | Por encima de este valor → `URGENT_SWITCH` |
| Max cost spike multiplier | `urgent_triggers.max_cost_spike_multiplier` | `3.0` | Usado para el umbral de color de pico de costo en la pestaña Tracking |

### Sobreescrituras por agente

Cualquier umbral en `defaults` puede sobreescribirse por agente bajo `per_agent.<agentID>`:

```json
{
  "per_agent": {
    "sdd-verify": {
      "min_accuracy": 0.95
    }
  }
}
```

Solo los campos no nulos sobreescriben el valor predeterminado; los campos faltantes heredan de `defaults`.

### Precios de modelos

El mapa `model_pricing.models` lista los precios de salida de los modelos por millón de tokens. Un valor de `0.0` marca un modelo como gratuito; los modelos ausentes se tratan como de pago.

```json
{
  "model_pricing": {
    "models": {
      "gemma-2-9b-free": 0.0,
      "opencode/claude-sonnet-4-6": 15.0
    }
  }
}
```

Los modelos gratuitos omiten las verificaciones de ROI y costo en el motor de decisiones. Aún pueden recibir veredictos `SWITCH` o `URGENT_SWITCH` basados en accuracy o tasa de errores.

---

## Tipos de ejecución de benchmark

| Tipo | Disparador | Ventana |
|------|------------|---------|
| `weekly` | Lunes 02:00 hora local (cron `"0 0 2 * * 1"`) | Últimos 7 días desde `now` |
| `intraweek` | `F5` en la pestaña **Benchmark Detailed** | Desde `last_run_at + 1ms` hasta `now` (regresa a 7 días si no hay ejecución previa) |

Ambos tipos usan la misma implementación `Runner.run()` y producen filas `BenchmarkRun` idénticas en `benchmark.db`, etiquetadas con `run_kind`. Las ejecuciones intra-semana son útiles para obtener métricas actualizadas a mitad de semana sin esperar el programa del domingo.

---

---

## Determinación del modelo activo

En el momento en que se ejecuta cada benchmark, el runner determina qué modelo está actualmente activo para cada agente leyendo `~/.config/opencode/opencode.json`.

- El modelo configurado para el agente en `opencode.json` al momento del benchmark se marca como `run_status = 'active'`.
- Todos los demás modelos evaluados en el mismo ciclo (es decir, modelos que aparecieron en la ventana de eventos pero ya no están configurados) reciben `run_status = 'superseded'`.
- **Fallback**: si el agente no se encuentra en `opencode.json` (p. ej., un agente personalizado no declarado allí), el runner recurre a una heurística: el modelo con más eventos en la ventana de evaluación se trata como el modelo activo.

Esta determinación por ejecución se realiza en el momento de escritura: el modelo activo queda estampado en la fila de `benchmark_runs` al guardarse.

### Reemplazo entre ciclos (`MarkSupersededRuns`)

Cuando se completa una nueva ejecución para un agente, el runner también llama a `MarkSupersededRuns`: cualquier ejecución `active` anterior para ese agente cuyo modelo difiera del nuevo modelo activo se actualiza a `run_status = 'superseded'`. Esto asegura que, entre ciclos, solo la ejecución que refleja el modelo actualmente configurado tenga estado `active`.

---

## Estado de ejecución

Cada fila en `benchmark_runs` tiene un campo `run_status` con uno de dos valores:

| Valor | Significado |
|-------|-------------|
| `active` | Este modelo está actualmente configurado para el agente en `opencode.json`. Sus métricas se usan para mostrar el veredicto, el health score y la vista Benchmark History Summary. |
| `superseded` | Este modelo estuvo activo en un ciclo anterior pero ha sido reemplazado. Se muestra en el TUI con `—` en la columna de veredicto y no impulsa las decisiones SWITCH/KEEP. |

**Implicaciones de visualización en el TUI**:
- En la pestaña **Benchmark History Summary**, el marcador `●` indica la fila del modelo activo. El veredicto se muestra solo para esa fila; las filas reemplazadas muestran `—`.
- En la pestaña **Benchmark Detailed**, las filas reemplazadas se etiquetan como `CHANGED` en la columna de estado para indicar que el agente ha pasado a un modelo diferente.

---

## Evaluación por modelo

Cada ejecución de benchmark evalúa por separado cada **modelo distinto** utilizado por un agente. Los eventos se agrupan por `NormalizeModelName(e.Model)` antes de la agregación de métricas. Esto significa que:

- `opencode/claude-sonnet-4-6` y `opencode/claude-haiku-4-5` producen filas separadas para el mismo agente
- Si un agente cambió de modelo en mitad de la ventana, ambos modelos se evalúan de forma independiente
- La pestaña **Benchmark History Summary** muestra una fila por par `(agente, modelo)`, pero solo para pares activos en los últimos 4 ciclos semanales

---

## Tendencia de veredicto

La **tendencia de veredicto** muestra los últimos 5 veredictos para un par (agente, modelo) dado, calculados a partir de **ejecuciones semanales solamente** (`run_kind = 'weekly'`). Las ejecuciones intra-semana se excluyen de la tendencia para evitar distorsionarla con instantáneas de mitad de ciclo.

- La tendencia se muestra como una secuencia de símbolos de veredicto (p. ej., `K K K S K` para KEEP/SWITCH).
- `CHANGED` aparece en la tendencia cuando dos ejecuciones activas consecutivas del mismo agente usaron **modelos diferentes**. Esto señala que el agente cambió de modelo entre ciclos.
- Solo las ejecuciones donde `run_status = 'active'` contribuyen a la tendencia; las ejecuciones reemplazadas se excluyen.

---

## Visualización de tiempo

Todos los valores de tiempo de respuesta en el TUI se muestran en **formato humanizado** en lugar de milisegundos o segundos sin procesar. El formato se adapta a la magnitud de la duración:

| Rango de duración | Ejemplo de formato |
|-------------------|--------------------|
| ≥ 1 hora | `5h 11m` |
| ≥ 1 minuto | `24m 15s` |
| ≥ 1 segundo | `42.3s` |
| < 1 segundo | `850ms` |

Esto aplica a todas las columnas que muestran tiempos de turno promedio o percentil (p. ej., `Avg`, `P95`) en las pestañas Benchmark History Summary y Benchmark Detailed.

---

## Campo Raw Model

Cada fila de `benchmark_runs` almacena un campo `raw_model` que preserva el nombre completo del modelo con prefijo de proveedor tal como lo emitió el plugin (p. ej., `opencode/claude-sonnet-4-6`).

- El valor `raw_model` se muestra textualmente en el panel **Decision Rationale** (el panel de detalles en la pestaña Benchmark Detailed) para dar una identificación exacta del modelo.
- Las columnas de la tabla en ambas pestañas de benchmark muestran el nombre de modelo **normalizado** (prefijo eliminado, p. ej., `claude-sonnet-4-6`) por legibilidad.
- La normalización se realiza mediante `NormalizeModelName()`, que elimina el prefijo del proveedor (todo hasta e incluyendo la última `/`) de la cadena del modelo.

---

## Selección del mejor modelo alternativo

Cuando se emite un veredicto SWITCH o URGENT_SWITCH, el runner busca un mejor modelo **dentro de los datos de la ventana actual del mismo agente** antes de recurrir a recomendaciones basadas en la configuración.

Criterios de selección (orden de prioridad):

1. **Accuracy primero**: el candidato debe tener `accuracy > current - 0.001`
2. **ROI segundo**: entre igual accuracy, preferir mayor `ROIScore`
3. **Velocidad tercero**: entre igual accuracy y ROI, preferir menor `AvgTurnMs`

El candidato también debe tener `SampleSize >= 50` y no debe ser `URGENT_SWITCH` por sí mismo.

```go
// internal/runner/runner.go
func bestAlternativeModel(currentModel string, current benchmark.WindowMetrics, perModel map[string]modelMetrics) string
```

---

## Campos obsoletos

Los siguientes campos existen en `WindowMetrics` y `BenchmarkRun` por compatibilidad retroactiva, pero no portan información nueva:

| Campo | Estado | Nota |
|-------|--------|------|
| `ToolSuccessRate` | Obsoleto | Siempre 1.0 en la práctica; excluido de los disparadores SWITCH |
| `AvgQuality` | Obsoleto | `quality_score` rara vez se emite; no tiene influencia en los veredictos |
| `AvgLatencyMs` | Alias obsoleto | Se completa desde `AvgTurnMs` para ejecuciones antiguas |
| `P50LatencyMs` / `P95LatencyMs` / `P99LatencyMs` | Alias obsoletos | Se completan desde los percentiles de turno |
| `completedSegmentsCost` | Inactivo (plugin) | Nunca se escribe; se mantiene por compatibilidad de estructura |
| `lastStepCost` | Inactivo (plugin) | Nunca se escribe; se mantiene por compatibilidad de estructura |
| `MaxLatencyP95Ms` | Umbral inactivo | Presente en `DefaultThresholds` pero no se usa como disparador SWITCH |
| `MinToolSuccessRate` | Umbral inactivo | Presente en `DefaultThresholds` pero no se usa como disparador SWITCH |
