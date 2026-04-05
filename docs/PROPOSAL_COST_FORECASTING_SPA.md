> Esta es la traducción al español de PROPOSAL_COST_FORECASTING.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Propuesta SDD: Pronóstico de Costos y Alertas de Presupuesto

**Fecha**: 4 de abril de 2026  
**Funcionalidad**: Pronóstico de Costos y Alertas de Presupuesto para metronous  
**Estado**: Propuesta (pendiente de revisión)

---

## Intención

Metronous rastrea costos de modelos de IA y agentes, pero no ofrece visibilidad sobre el gasto futuro ni gestión de presupuesto. Los equipos carecen de herramientas para predecir tendencias de costo, establecer límites de gasto o recibir alertas antes de que ocurran excesos presupuestarios. Esta funcionalidad aborda tres brechas críticas:

1. **Predicción de costos**: pronosticar el gasto para los próximos 7-30 días basándose en tendencias históricas, lo que permite una planificación presupuestaria proactiva.
2. **Control del presupuesto**: definir límites estrictos (por agente, por modelo o globales) con alertas claras cuando se acercan a los umbrales.
3. **Conciencia del costo**: mostrar tendencias de costo en la pestaña Charts con indicadores visuales y alertas en el TUI, haciendo visibles los patrones de gasto de un vistazo.

Esto reduce las sorpresas en las facturas, permite decisiones de optimización de costos y mejora la visibilidad financiera del equipo en la infraestructura de IA.

---

## Alcance

### Funcionalidades (lo que verán los usuarios)

- **Configuración de límites de presupuesto**: establecer presupuestos via `thresholds.json` para costos globales, por agente o por modelo (diarios, semanales, mensuales).
- **Superposición de pronóstico de costos**: nueva sección en la pestaña Charts que muestra pronósticos de 7/14/30 días con bandas de confianza (límites superior/inferior).
- **Alertas de presupuesto**: banners en el TUI (parte superior del dashboard) cuando el costo tiende hacia o ha superado los límites de presupuesto; también se registran en `metronous.log`.
- **Métricas de precisión del pronóstico**: comparación histórica de pronóstico vs. real para que los usuarios evalúen la calidad del pronóstico.
- **Desglose de costos por agente**: gráfico de pronóstico desglosado por agente para identificar qué agentes impulsan costos elevados.

### Módulos afectados

| Módulo | Cambios |
|--------|---------|
| `internal/store` | 3 nuevas tablas: `daily_cost_snapshots`, `cost_forecasts`, `budget_alerts` |
| `internal/runner` | Tarea de generación de pronóstico semanal (en el pipeline de benchmark) |
| `internal/tui` | Pestaña Charts extendida con panel de superposición para pronósticos |
| `internal/config` | Nuevos campos en `thresholds.json`: `budget_limits`, `forecasting`, `alerts` |
| `internal/cli` | Nuevo comando: `metronous config budgets [get|set]` para gestionar límites |
| `internal/decision` | Opcional: recomendaciones de modelo basadas en costo (alternativas de menor costo cuando se supera el presupuesto) |

### Fuera del alcance

- Notificaciones por email/Slack (las alertas se muestran solo en el TUI; el registro se proporciona para sistemas externos).
- Aislamiento de presupuesto multi-tenant (todos los presupuestos son por instancia local).
- Reducción automática de costos (p. ej., cambio de modelos cuando se supera el presupuesto).
- Informes históricos de precisión de pronóstico (rastreados pero no visualizados en v1).
- Métodos de pronóstico avanzados más allá de EMA (Holt-Winters disponible como ruta de actualización).

### Dependencias

- **VividCortex/ewma**: librería Go para el cálculo de Media Móvil Exponencial.
- **jthomperoo/holtwinters**: actualización opcional para pronóstico estacional avanzado (diferido a v2).
- **`QueryDailyCostByModel` existente**: base para las instantáneas diarias; sin cambios en la firma.

---

## Enfoque

### Diseño del esquema

#### 1. `daily_cost_snapshots` (tabla de datos principal)
Almacena totales de costo diario calculados desde los eventos. Llenada por el runner al final del día.

```sql
CREATE TABLE daily_cost_snapshots (
  id INTEGER PRIMARY KEY,
  date DATE UNIQUE NOT NULL,                 -- Fecha [desde, hasta) = [date, date+1day)
  total_cost_usd DECIMAL(12, 4) NOT NULL,    -- Costo total de todos los modelos/agentes
  model_breakdown TEXT NOT NULL,             -- JSON: {"gpt-4": 12.34, "claude-3": 5.67}
  agent_breakdown TEXT NOT NULL,             -- JSON: {"agent-a": 8.00, "agent-b": 10.01}
  snapshot_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**Razonamiento**: Los desgloses desnormalizados evitan consultas de agregación repetidas; JSON permite dimensionalidad flexible.

#### 2. `cost_forecasts` (resultados de predicción)
Almacena pronósticos calculados. Se regenera semanalmente (cada lunes, 00:00 UTC).

```sql
CREATE TABLE cost_forecasts (
  id INTEGER PRIMARY KEY,
  forecast_date DATE NOT NULL,               -- Fecha en que se calculó este pronóstico
  forecast_horizon INTEGER NOT NULL,         -- Días adelante: 7, 14 o 30
  forecast_type TEXT NOT NULL,               -- 'ema' o 'holtwinters' (v2)
  target_date DATE NOT NULL,                 -- La fecha que se está pronosticando
  predicted_cost_usd DECIMAL(12, 4) NOT NULL,
  confidence_lower_usd DECIMAL(12, 4),       -- Límite inferior de la banda de confianza del 80%
  confidence_upper_usd DECIMAL(12, 4),       -- Límite superior de la banda de confianza del 80%
  model_forecasts TEXT,                      -- JSON: {"gpt-4": {...}, "claude-3": {...}}
  actual_cost_usd DECIMAL(12, 4),            -- Se completa después de que pasa target_date
  error_pct DECIMAL(5, 2),                   -- |actual - predicted| / actual * 100
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(forecast_date, forecast_horizon, target_date)
);
```

**Razonamiento**: Las bandas de confianza permiten una planificación presupuestaria consciente del riesgo; el seguimiento de errores mejora el pronóstico con el tiempo; los pronósticos de modelo separados para visibilidad del desglose.

#### 3. `budget_alerts` (registro de alertas)
Registro de auditoría de violaciones de límites de presupuesto y advertencias. Se usa para la deduplicación de alertas.

```sql
CREATE TABLE budget_alerts (
  id INTEGER PRIMARY KEY,
  alert_type TEXT NOT NULL,                  -- 'warning' (80%), 'critical' (95%), 'exceeded'
  scope TEXT NOT NULL,                       -- 'global', 'agent:{name}', 'model:{name}'
  budget_limit_usd DECIMAL(12, 4) NOT NULL,
  current_cost_usd DECIMAL(12, 4) NOT NULL,
  forecast_cost_usd DECIMAL(12, 4),          -- NULL si es gasto excedido real
  alert_date DATE NOT NULL,                  -- Fecha en que se activó la alerta
  period TEXT NOT NULL,                      -- 'daily', 'weekly', 'monthly'
  dismissed_at DATETIME,                     -- NULL hasta que el usuario reconozca
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_alert_date_scope (alert_date, scope)
);
```

**Razonamiento**: Soporta deduplicación de alertas (no repetir la misma alerta), seguimiento de descarte y registros de auditoría para revisiones de costos.

### Elección del algoritmo: Media Móvil Exponencial (EMA)

**Seleccionado**: EMA con α (alpha) = 0.3  
**Razonamiento**:
- **Simplicidad**: un solo parámetro, fácil de ajustar; sin componentes estacionales/de tendencia para modelar por separado.
- **Capacidad de respuesta**: α=0.3 da 70% de peso a los datos recientes, 30% a los históricos; captura cambios de tendencia rápidamente.
- **Robustez**: funciona bien con datos escasos (p. ej., 2-3 semanas de historial); no requiere datos mínimos.
- **Cálculo en tiempo real**: actualizaciones O(1); no se necesita reentrenamiento por lotes.
- **Explicabilidad**: los usuarios no técnicos pueden entender "pronóstico basado en tendencia reciente".

**¿Por qué no Holt-Winters?** Requiere 4-6 semanas de datos (2 ciclos estacionales) y es excesivo para la funcionalidad inicial; agregado como ruta de actualización a v2 con la columna `forecast_type` lista.

**Bandas de confianza**: intervalo de confianza del 80% calculado como:
```
std_dev = sqrt(sum((actual[i] - forecast[i])^2) / n)
lower = forecast - 1.28 * std_dev   # Puntuación z del percentil 80
upper = forecast + 1.28 * std_dev
```

### Flujo de integración

```
┌─────────────────────────────────────────────────────────────────┐
│ Runner (semanal, lunes 00:00 UTC)                               │
│ ───────────────────────────────────────────────────────────────│
│ 1. Obtener instantáneas diarias de los últimos 30 días via QueryDailyCostByModel
│ 2. Calcular pronósticos EMA para horizontes de 7/14/30 días (almacenar en cost_forecasts)
│ 3. Verificar límites de presupuesto contra pronósticos; registrar violaciones en budget_alerts
│ 4. Activar banner de alerta en el TUI para presupuestos críticos
└──────────────┬──────────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────────────┐
│ Pestaña Charts del TUI (actualización bajo demanda, manual o automática) │
│ ──────────────────────────────────────────────────────────────── │
│ • Vista principal: tendencia de costo histórica (últimos 30 días como barras/líneas) │
│ • Panel de superposición: pronóstico de 7/14/30 días con bandas de confianza │
│ • Desglose por agente: costos pronosticados por agente debajo del gráfico principal │
│ • Banner de alerta: "Warning: Global budget trending 85% → $5K" │
└──────────────────────────────────────────────────────────────────┘
```

### Modelo de configuración

Agregar a `thresholds.json`:

```json
{
  "budgets": {
    "enabled": true,
    "global": {
      "daily": 500.00,
      "weekly": 3000.00,
      "monthly": 10000.00
    },
    "agents": {
      "researcher-bot": {
        "daily": 100.00,
        "monthly": 2000.00
      },
      "api-tester": {
        "daily": 50.00
      }
    }
  },
  "forecasting": {
    "enabled": true,
    "method": "ema",
    "alpha": 0.3,
    "min_data_points": 10,
    "forecast_horizons": [7, 14, 30],
    "regenerate_schedule": "0 0 * * 1"  // Cron: lunes 00:00 UTC
  },
  "alerts": {
    "enabled": true,
    "warning_threshold_pct": 80,
    "critical_threshold_pct": 95,
    "deduplicate_hours": 24,
    "show_in_tui": true,
    "log_to_file": true
  }
}
```

### Ciclo de vida de los datos

- **daily_cost_snapshots**: retener 90 días; eliminar automáticamente los más antiguos. Se actualiza nocturnamente por el runner.
- **cost_forecasts**: retener 365 días; eliminar automáticamente los más antiguos. Se regenera semanalmente.
- **budget_alerts**: retener 180 días; eliminar automáticamente los más antiguos. Se mantienen para auditoría y análisis de tendencias.

---

## Compromisos

### 1. EMA vs Holt-Winters

| Factor | EMA | Holt-Winters |
|--------|-----|--------------|
| Requisito de datos | ≥5 puntos | ≥20-30 puntos (2 temporadas) |
| Precisión (datos maduros) | 85-90% MAPE | 92-95% MAPE |
| Complejidad | Baja (1 parámetro) | Alta (3 parámetros) |
| Esfuerzo de ajuste | Mínimo | Significativo |
| Tiempo de implementación | 2 horas | 2 días (via librería) |
| **Decisión** | ✅ **Seleccionado para v1** | Ruta de actualización para v2 |

**Razonamiento**: Los usuarios que despliegan metronous pueden tener solo 2-3 semanas de datos de costo. EMA funciona de inmediato; Holt-Winters requiere paciencia y ajuste.

### 2. Ubicación en la interfaz: Superposición vs Nueva pestaña vs Barra lateral

| Opción | Ventajas | Desventajas | **Decisión** |
|--------|----------|-------------|----------|
| **Nueva pestaña** | Separación clara, UX dedicada | Sobrecarga de navegación; aumenta la carga cognitiva | ❌ No |
| **Widget de barra lateral** | Siempre visible; conciencia pasiva | Reduce el espacio del gráfico; puede distraer | ❌ No |
| **Superposición en pestaña Charts** | Se integra con los datos de costo existentes; activar/desactivar | Algo comprimido en terminales pequeños | ✅ **Seleccionado** |

**Razonamiento**: La pestaña Charts ya muestra costos históricos; la superposición de pronóstico es una extensión natural. Activar/desactivar el pronóstico (via atajo de teclado) da a los usuarios una opción sin explosión de pestañas.

### 3. Frecuencia de alertas y deduplicación

| Estrategia | Frecuencia | Ventajas | Desventajas |
|------------|------------|----------|-------------|
| **Por actualización** | Cada ejecución de pronóstico (semanal) | Precisa | Fatiga de alertas; ruido |
| **Por día** | Una vez por día calendario | Razonable | Puede perderse picos críticos |
| **Por semana** | Una vez a la semana | Tranquila | Notificación retrasada |
| **Solo manual** | El usuario ve el dashboard | Sin spam | Fácil de perder alertas |

**Decisión**: Enfoque híbrido:
- Calcular alertas en el momento del pronóstico (semanal).
- Mostrar en el TUI de inmediato (banner, descartable).
- Deduplicar por scope + presupuesto: no re-alertar el mismo presupuesto en el mismo día a menos que cambie la severidad (p. ej., advertencia → crítico).
- Registrar todo en la tabla `budget_alerts` para auditoría.

**Razonamiento**: pronóstico semanal + banner TUI descartable + registro = visibilidad de alertas sin spam.

### 4. Presupuestos por agente vs globales

| Enfoque | Ventajas | Desventajas | **Decisión** |
|---------|----------|-------------|----------|
| **Solo global** | Simple; un solo límite | Sin control por equipo/agente | ❌ No |
| **Solo por agente** | Control detallado | Configuración compleja; difícil de aplicar límites globales | ❌ No |
| **Jerárquico (global + por agente)** | Flexible; soporta ambos | Más código; validación de configuración necesaria | ✅ **Seleccionado** |

**Razonamiento**: presupuesto global = red de seguridad; por agente = responsabilidad del equipo. El esquema de configuración soporta ambos; el runner verifica ambos y alerta ante cualquier violación.

### 5. Pronóstico en tiempo real vs por lotes

| Enfoque | Latencia | Costo | Precisión | **Decisión** |
|---------|---------|-------|-----------|----------|
| **Tiempo real** | Inmediato (bajo demanda) | O(1) por pronóstico | Obsoleto hasta la próxima ejecución | ❌ No |
| **Lotes semanales** | Hasta 7 días obsoleto | O(n) una vez/semana | Fresco después de la ejecución | ✅ **Seleccionado** |
| **Lotes diarios** | Hasta 1 día obsoleto | O(n) diario | Mejor frescura | ⚠️ Fase 2 |

**Razonamiento**: los lotes semanales son suficientes para la planificación presupuestaria (los costos no cambian de minuto a minuto). Los lotes diarios pueden agregarse en la Fase 2 si los usuarios solicitan mayor frescura.

---

## Plan de reversión

### Estrategia de eliminación segura

La funcionalidad está diseñada para fallar de manera elegante si las tablas no existen:

1. **Versiones de migración**: cada creación de tabla es una migración separada con número de versión (p. ej., `001_add_cost_snapshots.sql`).
2. **Desactivación suave**: el flag de configuración `budgets.enabled` y `forecasting.enabled` permiten deshabilitar sin cambios de código.
3. **Comportamiento de fallback**:
   - Si falta la tabla `cost_forecasts`: la pestaña Charts muestra solo datos históricos (sin superposición de pronóstico).
   - Si falta la tabla `budget_alerts`: no se registran violaciones de presupuesto, pero el runner continúa normalmente.
   - Si falta la tabla `daily_cost_snapshots`: el pronóstico se deshabilita, pero `QueryDailyCostByModel` histórico sigue funcionando.

### Revertir la implementación

**Paso 1: Deshabilitar en la configuración** (sin tiempo de inactividad):
```json
{
  "budgets": { "enabled": false },
  "forecasting": { "enabled": false },
  "alerts": { "enabled": false }
}
```

**Paso 2: Eliminar tablas** (fuera de línea):
```bash
sqlite3 ~/.metronous/data/tracking.db << EOF
DROP TABLE budget_alerts;
DROP TABLE cost_forecasts;
DROP TABLE daily_cost_snapshots;
EOF
```

**Paso 3: Verificar**: reiniciar metronous; confirmar que la pestaña Charts muestre solo datos históricos.

### Compatibilidad de migración

- **Hacia arriba**: crear tablas en secuencia; el runner verifica la existencia antes de poblar.
- **Hacia abajo**: eliminar tablas en orden inverso; sin mutación del esquema de las tablas existentes.
- **Sin cambios incompatibles**: las tablas `events` y `benchmarks` existentes no se tocan; `QueryDailyCostByModel` no se modifica.

---

## Riesgos y mitigación

### Riesgo 1: Escasez de datos (pronóstico no confiable)

**Problema**: Los nuevos despliegues pueden tener solo 2-3 días de datos de costo; los pronósticos EMA sobre datos escasos son ruido.

**Mitigación**:
- Requerir mínimo 10 puntos de datos antes de generar cualquier pronóstico (configuración: `forecasting.min_data_points`).
- Ampliar las bandas de confianza para datos escasos: confianza = 50% para <10 puntos, 80% para ≥20 puntos.
- Mostrar advertencia en Charts: "Forecast requires 10+ days of data; currently X days available."
- En el runner: omitir la generación de pronósticos y registrar advertencia si no se cumple el umbral.

**Pruebas**: pruebas impulsadas por tabla con 1/5/10/30 puntos de datos; verificar que el pronóstico no se genere o esté marcado como de baja confianza.

### Riesgo 2: Alertas de presupuesto falsas (falsos positivos)

**Problema**: Las variaciones de costo (días de cero costo en fin de semana) podrían activar alertas espurias de "presupuesto excedido".

**Mitigación**:
- Usar el límite superior de confianza (no la estimación puntual) para las verificaciones de presupuesto: `si actual > presupuesto O forecast_upper > presupuesto, alertar`.
- Implementar deduplicación de alertas: no volver a activar la misma alerta (scope + presupuesto) dentro de 24 horas a menos que la severidad aumente.
- Distinguir tipos de alerta: "warning" (80% de confianza excedido), "critical" (95%), "exceeded" (gasto excedido real).
- Registrar todas las decisiones en la tabla `budget_alerts` con el campo `dismissed_at`; los usuarios pueden revisar los falsos positivos.

**Pruebas**: historial de costos simulado con picos de fin de semana; verificar que el conteo de alertas sea <5 por mes por presupuesto.

### Riesgo 3: Fatiga de alertas

**Problema**: Demasiadas alertas → los usuarios las ignoran.

**Mitigación**:
- Agrupar alertas: un banner TUI por ejecución (no por presupuesto).
- Ventana de deduplicación: 24 horas (no repetir la misma alerta).
- Descartable: el usuario puede cerrar el banner; se registra pero no se muestra nuevamente hasta el día siguiente.
- Parámetro de configuración: `alerts.deduplicate_hours` permite ajuste específico del sitio.

**Métrica de éxito**: <5 alertas/día/usuario en operación normal (probar con datos sintéticos).

### Riesgo 4: Cambios de precios de modelos

**Problema**: Los cambios de precio a mitad de mes (p. ej., actualización de precios de GPT-4) rompen la tendencia histórica.

**Mitigación**:
- Documentar en thresholds.json: al actualizar costos de modelos, anotar la fecha del cambio.
- Runner: verificar datos de costo después de la fecha del cambio de precio; si <5 puntos, no pronosticar.
- Opción (v2): resetear manualmente el estado del pronóstico ante un cambio de precio.

**La mitigación es procedimental, no técnica**: depende de la conciencia del usuario (las notas del changelog indican las actualizaciones de precio).

### Riesgo 5: Deriva del pronóstico con el tiempo

**Problema**: EMA α=0.3 puede sub/sobreestimar sistemáticamente si la tendencia cambia (p. ej., nuevo modelo de alto costo adoptado).

**Mitigación**:
- Rastrear el error de pronóstico (actual - predicho) en `cost_forecasts.error_pct`.
- Métrica del dashboard: "Forecast MAPE last 30 days: 12%" (visible para el usuario).
- Si MAPE >25% durante >10 pronósticos consecutivos, sugerir ajuste del alpha o deshabilitar el pronóstico.
- v2: auto-ajuste del alpha o cambio a Holt-Winters si se activa la detección.

**Pruebas**: simular cambios de tendencia (introducir nuevo modelo a mitad de flujo); verificar que la alerta siga funcionando y el error crezca de manera controlada.

### Riesgo 6: Impacto en el rendimiento (cálculo de pronósticos)

**Problema**: El cálculo EMA + escrituras en base de datos sobre grandes conjuntos de datos históricos podría ralentizar el runner o la actualización del TUI.

**Mitigación**:
- Cálculo EMA: una sola pasada O(n) sobre 30 días de datos = <100ms en máquinas típicas.
- Inserciones en base de datos: lote de 3 pronósticos (7/14/30 días) + 5 alertas por ejecución = E/S mínima.
- Caché: cachear los últimos 30 días de instantáneas en memoria durante la ejecución del pronóstico; evitar consultas repetidas.
- Benchmark: `go test -bench=BenchmarkForecastGeneration` asegura <500ms incluso con 1 año de datos.

**Pruebas**: prueba de rendimiento con historial de costos sintético de 1 año; objetivo <1s de extremo a extremo.

---

## Estimación de esfuerzo

### Desglose por fase (4-6 semanas en total, 1 desarrollador)

| Fase | Tareas | Horas | Dependencias |
|------|--------|-------|--------------|
| **Fase 1: Esquema y migraciones** | Crear 3 tablas, escribir migraciones, probar reversión. | 8 | Ninguna |
| **Fase 2: EMA y operaciones de store** | Implementar EMA, agregar métodos de store (InsertSnapshot, QueryForecasts), pruebas unitarias. | 16 | Fase 1 |
| **Fase 3: Integración con el runner** | Tarea de pronóstico semanal, generación de alertas, registro, prueba de extremo a extremo. | 12 | Fase 2 |
| **Fase 4: Superposición en Charts del TUI** | Extender la pestaña Charts, agregar panel de pronóstico, enlace de tecla para activar/desactivar, refinamiento de UX. | 16 | Fase 3 |
| **Fase 5: Config y CLI** | Agregar campos en thresholds.json, validación de configuración, comandos CLI (get/set budgets), docs. | 12 | Fase 3 |
| **Fase 6: Pruebas y documentación** | Pruebas de integración, benchmarks de rendimiento, guía de usuario, cobertura de casos límite. | 12 | Todas las anteriores |

**Total**: 76 horas ≈ 2.2 semanas @ 40h/semana, o ≈ 4 semanas @ 20h/semana (considerando revisión/correcciones/integración).

**Ruta crítica**:
```
Fase 1 → Fase 2 → Fase 3 ──┬→ Fase 4 ─┐
                   └──→ Fase 5 ─┴→ Fase 6
```

Las Fases 4 y 5 pueden ejecutarse en paralelo después de la Fase 3; la Fase 6 (pruebas/documentación) es la cola de la ruta crítica.

---

## Criterios de éxito

### Métricas cuantitativas

| Métrica | Objetivo | Cómo medir |
|---------|---------|---|
| **Precisión del pronóstico (MAPE)** | <15% en horizonte de 30 días | Ejecutar contra 90 días de datos reales; comparar `error_pct` en `cost_forecasts` |
| **Tasa de falsos positivos** | <5% (≤1 alerta espuria por cada 20 reales) | Auditoría manual de la tabla `budget_alerts` durante un período de 2 semanas |
| **Tiempo de cálculo** | <500ms por ejecución de pronóstico | Prueba de benchmark con historial de 1 año; tiempo de tarea semanal del runner |
| **Latencia de consulta** (superposición Charts) | <200ms de actualización | Medir consultas `SELECT * FROM cost_forecasts`; cachear si es necesario |
| **Adopción por usuarios** | ≥50% habilitan presupuesto | Encuesta/sondeo a usuarios después de 2 semanas; verificar `budgets.enabled` en configs |
| **Tasa de descarte de alertas** | ≥80% descartadas en <24h | Registrar eventos de descarte; medir tiempo desde la alerta hasta el descarte |

### Métricas cualitativas

- **Usabilidad**: los usuarios pueden establecer presupuestos y entender los pronósticos en <5 minutos (validar en entrevista con usuario).
- **Confiabilidad**: el pronóstico es con suficiente frecuencia preciso para que los usuarios lo referencien en reuniones de planificación presupuestaria.
- **Documentación**: todos los nuevos campos de configuración, comandos CLI y algoritmos documentados en README y comentarios de código en línea.

### Historias de éxito (post-lanzamiento)

1. **El usuario evita exceder el presupuesto**: establece presupuesto global de $10K/mes; el pronóstico lo alerta a $8K; optimiza los agentes y se mantiene dentro del presupuesto.
2. **Adopción del desglose de costos**: los presupuestos por agente impulsan la responsabilidad; el equipo de ingeniería nota agentes de alto costo y migra a modelos más económicos.
3. **Decisiones impulsadas por pronósticos**: el equipo usa el pronóstico de 30 días para planificar el alcance del sprint: "Tenemos $500 de presupuesto restante, podemos ejecutar 10 rondas más de benchmark."

---

## Lista de verificación de implementación (para la siguiente fase: escritura de especificaciones)

- [ ] Definir esquemas SQL exactos con índices y restricciones.
- [ ] Especificar el algoritmo EMA con ejemplos detallados y manejo de casos límite.
- [ ] Definir la lógica de generación de alertas (qué presupuestos disparan, cómo se determina la severidad).
- [ ] Especificar la UX de superposición en Charts del TUI (atajos de teclado, renderizado).
- [ ] Definir comandos CLI y reglas de validación de configuración.
- [ ] Listar todos los escenarios de prueba (datos escasos, cambios de precio, deduplicación de alertas, rendimiento).
- [ ] Documentar el procedimiento de reversión y las propiedades de seguridad de la migración.

---

## Referencias

- **Referencia de EMA**: documentación de la librería Go VividCortex/ewma.
- **Intervalos de confianza**: https://en.wikipedia.org/wiki/Prediction_interval#Univariate_case
- **Mejores prácticas de pronóstico de costos**: AWS Cost Anomaly Detection (inspiración para la estrategia de deduplicación de alertas).
- **Código existente de metronous**: `internal/store/sqlite/event_store.go` (QueryDailyCostByModel), `internal/tui/charts_view.go` (pestaña Charts).

---

**Próximos pasos**: esperar comentarios de revisión. Una vez aprobado, proceder a la fase de escritura de especificaciones para especificaciones detalladas de requisitos y escenarios de prueba.
