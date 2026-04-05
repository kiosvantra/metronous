> Esta es la traducción al español de how-it-works.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Cómo Funciona Metronous

Metronous es un sistema local de telemetría, benchmarking y calibración de modelos para agentes de IA de OpenCode, con integración fluida para agentes construidos siguiendo la metodología SDD. Su objetivo es ayudar a los equipos a tomar decisiones basadas en datos sobre qué modelos de lenguaje usar para sus agentes, equilibrando precisión, latencia, uso de herramientas y costo.

Este documento explica la **metodología** detrás de Metronous: qué datos recopila, cómo agrega y puntúa los modelos, y cómo llega a veredictos SWITCH/KEEP accionables.

---

## Panorama general del flujo de datos

```
[OpenCode Agent] → (MCP via metronous mcp shim) → [Metronous Daemon (systemd service)]
                                                                   ↓
                                                         [SQLite Databases]
                                                                   ↓
                                                      [Benchmark Engine (weekly)]
                                                                   ↓
                                                   [Metrics, Scores, Verdicts]
                                                                   ↓
                                             [TUI Dashboard & CLI Reports]
```

1. **Ingestión de telemetría**  
   Cada vez que un agente invoca una herramienta (o inicia/termina una sesión), el plugin de OpenCode captura:
   - `agent_id`, `session_id`
   - `event_type` (`start`, `tool_call`, `complete`, `error`, etc.)
   - nombre del `model` (como está configurado en `opencode.json`)
   - `timestamp`
   - `input_tokens`, `output_tokens` (del proveedor LLM)
   - `cost_usd` (derivado de conteos de tokens × precio del modelo)
   - `quality_score` (opcional, de la propia validación del agente)
   - Payload arbitrario (p. ej., argumentos de herramienta, resultado)

   Este payload se envía como una solicitud MCP `tools/call ingest` al **metronous mcp shim**, que lo reenvía via HTTP al **daemon Metronous** de larga duración (un servicio de usuario systemd). El daemon escribe el evento en dos bases de datos SQLite:
   - `tracking.db` – flujo de eventos sin procesar (para la pestaña Tracking del TUI y consultas ad hoc)
   - `benchmark.db` – datos pre-agregados usados por el motor de benchmark semanal

2. **Esquema de almacenamiento (simplificado)**  
   ```sql
   -- tracking.db.events
   id INTEGER PRIMARY KEY,
   agent_id TEXT NOT NULL,
   session_id TEXT NOT NULL,
   event_type TEXT NOT NULL,
   model TEXT NOT NULL,
   timestamp DATETIME NOT NULL,
   input_tokens INTEGER,
   output_tokens INTEGER,
   cost_usd REAL,
   quality_score REAL,
   payload JSON
   ```

   La base de datos `benchmark.db` contiene tablas de resumen (`agent_summaries`, `benchmark_runs`) que se actualizan de forma incremental a medida que llegan nuevos eventos.

3. **Pipeline de benchmark semanal**  
    Por defecto, Metronous ejecuta un análisis de benchmark cada domingo a las 02:00 hora local (configurable via el TUI o variable de entorno). El pipeline consta de cuatro etapas:

    **Alineación de ciclos de ejecución (TUI):** la pestaña "Benchmark Detailed" agrupa los resultados en *semanas delimitadas por domingo* en *hora local*; esto es lo que PgUp/PgDn navega. La pestaña "Benchmark History Summary" muestra solo pares (agente, modelo) activos en los últimos 4 ciclos semanales, ponderados por recencia.

    **Modelo activo y estado de ejecución:** en el momento de la ejecución, el pipeline lee `~/.config/opencode/opencode.json` para determinar qué modelo está actualmente configurado para cada agente. La fila de ese modelo queda estampada con `run_status = 'active'`; todos los demás modelos en el mismo ciclo reciben `run_status = 'superseded'`. El nombre completo del modelo con prefijo de proveedor se almacena en `raw_model` para identificación exacta, mientras que las tablas usan el nombre normalizado (sin prefijo).

    ### a. Recopilación de datos  
    Para cada modelo visto en la ventana de tiempo seleccionada (predeterminado: últimos 7 días), se recopila:
   - Número total de eventos (`N`)
   - Suma de tokens de entrada y salida
   - Suma de `cost_usd`
   - Conteo de eventos con `quality_score` no nulo
   - Suma de `quality_score` (solo sobre eventos donde está presente)
   - Medidas de latencia (duración de extremo a extremo de sesiones de agente o llamadas a herramientas, según la configuración)
   - Tasa de uso de herramientas (fracción de eventos que son `tool_call`)
    - Métricas de resultado: proporción de veredictos `KEEP` vs `SWITCH` emitidos por el agente (si aplica)

    - **Filtro de descubrimiento:** los agentes que son efectivamente solo-error se excluyen del descubrimiento de benchmark para evitar marcadores de posición como `opencode/unknown`.

   ### b. Normalización  
   Cada métrica sin procesar se convierte a una **puntuación en [0, 1]** para poder combinar unidades dispares.  
   - Para métricas donde **mayor es mejor** (accuracy, tasa de uso de herramientas, quality score):  
     `score = (value − min) / (max − min)`  
   - Para métricas donde **menor es mejor** (latencia, costo):  
     `score = (max − value) / (max − min)`  

   `min` y `max` se calculan **a través de todos los modelos evaluados en la misma ventana**. Esto hace que la puntuación sea *relativa*: la puntuación de un modelo refleja cómo se desempeña en comparación con los otros modelos probados recientemente, no contra un ideal absoluto.

   Caso especial: si todos los modelos tienen el mismo valor para una métrica (max = min), cada modelo recibe una puntuación de `0.5` (el punto medio) para evitar la división por cero.

   ### c. Puntuación ponderada  
   Cada modelo recibe una puntuación final:
   ```
   final_score =
       w_acc   * score_accuracy   +
       w_lat   * score_latency    +
       w_tool  * score_tool_rate  +
       w_cost  * score_cost       +
       w_qual  * score_quality    (si quality_score es rastreado)
   ```
   Los pesos (`w_*`) están definidos en `internal/config/thresholds.json` bajo la clave `weights`. Deben sumar 1.0 (el sistema renormalizará si no lo hacen, pero es mejor mantenerlos normalizados).  
   Pesos de ejemplo de la configuración predeterminada:
   ```json
   "weights": {
     "accuracy": 0.40,
     "latency":  0.30,
     "tool":     0.10,
     "cost":     0.10,
     "quality":  0.10
   }
   ```

    Los precios pueden afectar los *disparadores de decisión* (SWITCH/URGENT_SWITCH), no solo las puntuaciones sin procesar. Las reglas de precios están definidas en `model_pricing` en `thresholds.json`.
 
    ### d. Umbrales de decisión  
    El sistema no declara un SWITCH simplemente porque un modelo tenga una puntuación más alta. Requiere una **mejora mínima** para evitar oscilaciones por diferencias insignificantes.

   - Sea `S_base` la puntuación del modelo de referencia actualmente activo para el agente que se está evaluando.
   - Sea `S_cand` la puntuación del modelo candidato que se está evaluando.
   - Calcula `delta = S_cand − S_base`.

   Entonces:
   - Si `delta > switch_threshold` → **SWITCH** al modelo candidato.
   - Si `delta < −keep_threshold` → **KEEP** la referencia (el candidato es significativamente peor).
   - De lo contrario → **INSUFFICIENT_DATA** (o efectivamente KEEP si la interfaz lo trata como tal).  
     Valores predeterminados típicos: `switch_threshold = 0.05`, `keep_threshold = 0.03`.

    Estos umbrales también son configurables en `thresholds.json`.

    ### Comportamiento con precios (modelos gratuitos y neutralidad ROI)
    Metronous aplica las siguientes sobreescrituras al decidir si activar SWITCH/URGENT_SWITCH:
    - **Modelos gratuitos (price == 0):** los disparadores basados en ROI/costo se omiten (el ROI se ignora para estos modelos).
    - **ROI no confiable (TotalCostUSD == 0):** el ROI se neutraliza; las señales de calidad/latencia/herramienta dominan.

4. **Propagación del veredicto**  
   El motor de benchmark escribe el modelo ganador (o la directiva de mantener el actual) en `~/.metronous/thresholds.json` bajo la clave `active_model`.  
   El TUI y la CLI leen este archivo para mostrar la recomendación activa.  
   OpenCode en sí **no** cambia de modelo automáticamente. Metronous reporta una recomendación; el usuario o equipo decide si actualizar la configuración relevante de OpenCode.  
   El flujo de trabajo previsto es:
   1. Esperar a que se ejecute el benchmark semanal (o activarlo manualmente con `metronous benchmark --model <name>`).
   2. Observar el veredicto en el TUI (pestaña Benchmark History Summary o Benchmark Detailed) o via `metronous report`.
   3. Si el veredicto es `SWITCH`, actualizar manualmente el modelo o la configuración de agente de OpenCode correspondiente.
   4. Reiniciar OpenCode (o recargar su servidor MCP) para comenzar a usar el nuevo modelo.

   Este paso manual asegura que los equipos mantengan el control y puedan verificar el cambio en un entorno de prueba antes de implementarlo en producción.

5. **Por qué funciona esta metodología**  
   - **Robustez ante el ruido**: al agregar sobre una ventana y normalizar en relación con los pares, Metronous filtra las variaciones cotidianas (p. ej., latencia de red temporal, complejidad variable de los prompts).  
   - **Compromisos accionables**: la suma ponderada fuerza una consideración explícita de precisión vs. velocidad vs. costo vs. uso de herramientas — compromisos que los equipos ya hacen intuitivamente pero que ahora ven cuantificados.  
    - **Decisiones con conciencia de precios**: los modelos gratuitos no son penalizados via disparadores de ROI/costo, y el ROI se neutraliza cuando los datos de costo no son confiables.
   - **Baja sobrecarga operativa**: una vez instalado como servicio systemd, Metronous se ejecuta en segundo plano con prácticamente ningún mantenimiento. La principal acción periódica es revisar el veredicto del benchmark y decidir si actualizar la configuración de modelos de OpenCode.  
   - **Extensibilidad**: se pueden agregar nuevas métricas (p. ej., huella de carbono, puntuaciones de seguridad) extendiendo el esquema de eventos, añadiendo una regla de normalización y asignando un peso — sin cambiar la lógica principal del pipeline.

6. **Limitaciones y compromisos de diseño**  
   - **Granularidad de la ventana**: la ventana semanal predeterminada puede ser demasiado lenta para equipos que quieran reaccionar a cambios dentro de un día. La ventana puede acortarse (p. ej., a 24h) via configuración, pero esto aumenta la varianza en las puntuaciones.  
   - **Supuesto de ponderación lineal**: el modelo asume que las métricas contribuyen de forma independiente y lineal a la utilidad general. La utilidad real puede tener interacciones (p. ej., una precisión por debajo de cierto umbral causa fallos catastróficos independientemente del bajo costo). Los equipos con fuerte conocimiento del dominio pueden ajustar los pesos o agregar reglas personalizadas fuera de Metronous.  
   - **Sin inferencia causal**: Metronous observa correlación, no causalidad. Un veredicto SWITCH podría estar impulsado por un cambio concurrente en los prompts, el pipeline de recuperación u otros factores ambientales. Para decisiones de alto riesgo, los equipos deben ejecutar una prueba A/B controlada o verificar manualmente el cambio antes de implementarlo ampliamente.  
   - **Dependencia del conteo preciso de tokens**: los cálculos de costo dependen de los conteos de tokens reportados por el proveedor LLM. Si un proveedor subreporta o sobrereporta tokens, la dimensión de costo quedará sesgada. La mayoría de los principales proveedores son precisos, pero vale la pena auditar si las estimaciones de costo parecen incorrectas.  
   - **Escasez de quality score**: muchos eventos (especialmente `tool_call` o `start`) no tienen `quality_score`. El sistema solo promedia la calidad sobre los eventos donde está presente; si la calidad rara vez se reporta, esta dimensión tiene poca influencia. Los equipos deben asegurarse de que sus agentes emitan una señal de calidad (p. ej., autoevaluación, resultado de prueba unitaria) para que la métrica sea significativa.

   - **Limitación de CSP en Web/Mobile**: las interfaces web y móvil de OpenCode están sujetas a restricciones de Content Security Policy (CSP) del navegador que bloquean conexiones a endpoints que no son del mismo origen (por ejemplo `http://127.0.0.1:<port>`). Por defecto, el CSP de OpenCode solo permite conexiones a `'self'` y `'data:'`, impidiendo que el shim MCP (`metronous mcp`) alcance al daemon Metronous local desde esos clientes. Para usar interfaces web/móvil, debes ajustar el CSP de OpenCode o desplegar un proxy inverso para hacer que el daemon parezca del mismo origen. Esta es una limitación del lado del cliente, no un problema del daemon Metronous.

7. **Cómo verificar o ajustar la metodología**  
   - **Inspeccionar datos sin procesar**: `sqlite3 ~/.metronous/data/tracking.db "SELECT * FROM events ORDER BY timestamp DESC LIMIT 20;"`  
   - **Verificar agregados**: `sqlite3 ~/.metronous/data/benchmark.dump` o usa la CLI: `metronous benchmark --debug` (imprime normalizaciones intermedias, pesos y delta).  
   - **Ajustar pesos/umbrales**: edita `~/.metronous/thresholds.json` (campos `weights`, `switch_threshold`, `keep_threshold`). Los cambios tienen efecto en la próxima ejecución de benchmark.  
   - **Cambiar la ventana de benchmark**: establece la variable de entorno `METRONOUS_BENCHMARK_WINDOW_DAYS=3` antes de ejecutar `metronous benchmark` o modifica la configuración del daemon.  
   - **Ejecutar benchmark manual**: `metronous benchmark --model gemma-2-9b-free --days 14` para evaluar un modelo específico sobre una ventana personalizada.

8. **Conclusión**  
   Metronous sacrifica cierta sofisticación analítica (p. ej., sin predicción de series temporales, sin modelos causales) a favor de la **simplicidad, transparencia y baja fricción operativa**. Su metodología está deliberadamente elegida para dar a los equipos una señal clara y accionable: *¿Hay un modelo que sea significativamente mejor en general, o debemos quedarnos con lo que tenemos?*  

   Al enfocarse en el rendimiento relativo dentro de una ventana reciente, normalizando en múltiples dimensiones y aplicando umbrales de decisión bien pensados, Metronous proporciona una brújula confiable para navegar el cambiante panorama de los modelos de lenguaje — sin demandar atención constante o conocimiento estadístico experto de sus usuarios.
