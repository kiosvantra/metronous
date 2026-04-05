> Esta es la traducción al español de architecture.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Panorama de la Arquitectura

Este documento describe la arquitectura en tiempo de ejecución de Metronous, enfocándose en componentes, protocolos de comunicación, flujo de datos y detalles de despliegue. Complementa [`how-it-works.md`](how-it-works.md), que cubre el enfoque metodológico del benchmarking y la recomendación de modelos.

## Tabla de Contenidos
- [Componentes principales](#componentes-principales)
- [Protocolos de comunicación](#protocolos-de-comunicación)
- [Flujo de datos](#flujo-de-datos)
- [Detalles del servicio Systemd](#detalles-del-servicio-systemd)
- [Manejo de fallos y resiliencia](#manejo-de-fallos-y-resiliencia)
- [Estructura de directorios](#estructura-de-directorios)
- [Extensibilidad](#extensibilidad)
- [Diagrama de secuencia (textual)](#diagrama-de-secuencia-textual)

---

## Componentes principales

| Componente | Responsabilidad | Tecnología | Notas |
|------------|-----------------|------------|-------|
| **OpenCode Plugin** | Captura eventos de telemetría de sesiones de agentes/llamadas a herramientas y los reenvía via MCP | TypeScript (incluido en `plugins/metronous-opencode/`) | Se ejecuta dentro de OpenCode; envía MCP `tools/call ingest` al shim |
| **metronous mcp shim** (`metronous mcp`) | Puente stdio↔HTTP; traduce mensajes stdio MCP a HTTP POST/GET al daemon | Go | Iniciado por el plugin de OpenCode por sesión; ligero, de corta duración por invocación |
| **Metronous Daemon** (`metronous server --daemon-mode`) | Servicio de usuario systemd de larga duración que ingesta telemetría, la almacena en SQLite, ejecuta benchmarks semanales y expone la API HTTP | Go | Administrado por systemd; una instancia por usuario; sobrevive reinicios de OpenCode |
| **SQLite Stores** | Almacenamiento persistente para eventos sin procesar (`tracking.db`) y datos de benchmark pre-agregados (`benchmark.db`) | SQLite via `internal/store/sqlite/` | Basado en archivos; ubicado en `~/.metronous/data/` |
| **CLI (`metronous` command)** | Comandos de usuario: `install`, `init`, `benchmark`, `report`, `dashboard`, etc. | Go/Cobra | Interactúa con el daemon via llamadas directas a funciones (cuando se ejecuta localmente) o HTTP (si el daemon es remoto — no soportado actualmente) |
| **TUI Dashboard** (`metronous dashboard`) | Interfaz de terminal que muestra cinco pestañas: Benchmark History Summary, Benchmark Detailed, Tracking, Charts y Config | Go/Bubbletea | Lee directamente desde archivos SQLite; presenta telemetría, gráficos de costos y resultados de benchmark |
| **OpenCode MCP Configuration** | Indica a OpenCode cómo alcanzar el shim | JSON en `~/.config/opencode/opencode.json` | Después de `metronous install`: `{ "mcp": { "metronous": { "command": ["/absolute/path/to/metronous", "mcp"], "type": "local" } } }` |

---

## Protocolos de comunicación

### 1. OpenCode → Shim (MCP sobre stdio)
- **Dirección**: plugin de OpenCode → shim (stdin/stdout del proceso `metronous mcp`)
- **Protocolo**: [Model Context Protocol (MCP)](https://modelcontextprotocol.io) sobre stdio, usando JSON-RPC 2.0
- **Mensajes**:
  - `tools/call` con `{ name: "ingest", arguments: { ...event payload... } }` (más común)
  - `tools/list` (para descubrir herramientas disponibles; el shim actualmente solo anuncia `ingest`)
  - `initialize` / `notifications/initialized` (manejado según la especificación)

### 2. Shim → Daemon (HTTP)
- **Dirección**: shim → daemon (HTTP POST/GET saliente del shim al daemon)
- **Protocolo**: HTTP/1.1 sobre TCP (solo loopback, `127.0.0.1`)
- **Endpoints**:
  - `POST /ingest` – recibe evento de telemetría (cuerpo JSON coincide con los argumentos de `ingest` de MCP)
  - `GET /health` – sonda de vivacidad (retorna `{status:"ok"}`)
  - `GET /status` – alias de `/health`
  - `GET /tools` – retorna la lista de herramientas soportadas (actualmente solo `ingest`)
- **Detalles**:
  - El shim lee el puerto desde `~/.metronous/data/mcp.port`
  - El shim realiza una verificación de salud (`GET /health`) antes de usar un puerto en caché para evitar conexiones al daemon muertas
  - El shim usa un timeout HTTP corto; ante un fallo trata al daemon como no saludable y reintenta via la ruta normal de inicio del daemon

### 3. Daemon → OpenCode (indirecto)
- El daemon **no** empuja datos a OpenCode. OpenCode obtiene información via:
  - TUI Dashboard (lee archivos SQLite directamente)
  - Comando CLI `metronous report`
  - Inspección manual de `~/.metronous/thresholds.json` (que contiene la recomendación de modelo activo después de una ejecución de benchmark)

### 4. CLI / TUI → Daemon (llamadas directas a funciones locales)
- Cuando la CLI o el TUI de `metronous` se ejecutan en la misma máquina, acceden a los archivos SQLite **directamente** (sin HTTP involucrado). Esto es más rápido y evita un salto adicional.
- El daemon, al ejecutarse como servicio systemd, mantiene los archivos SQLite abiertos; las lecturas concurrentes son seguras gracias al aislamiento de transacciones de solo lectura de SQLite y el modo WAL.

---

## Flujo de datos

1. **Captura de evento**  
   El plugin de OpenCode detecta un evento de agente (p. ej., `tool_call`) y construye un payload JSON que coincide con el esquema de la herramienta `ingest`.

2. **Transmisión MCP**  
   El plugin envía el evento como un mensaje MCP `tools/call ingest` al shim `metronous mcp` sobre stdio.

3. **Procesamiento en el shim**  
   - El shim analiza el mensaje MCP y extrae el payload JSON.
   - El shim lee el puerto actual del daemon desde `~/.metronous/data/mcp.port`.
   - El shim reenvía el payload como un HTTP POST a `http://127.0.0.1:<port>/ingest`.

4. **Ingestión en el daemon**  
    - El manejador HTTP del daemon (`ingestHandler`) recibe el POST, deserializa el JSON y lo pasa a `tracking.HandleIngest`.
    - `tracking.HandleIngest` escribe el evento en `tracking.db` (tabla de eventos) y actualiza los agregados de benchmark (via `upsertAgentSummary` dentro de una transacción).

5. **Almacenamiento**  
   - `tracking.db` almacena cada evento sin procesar (solo append, modo WAL para lectores/escritores concurrentes).
   - `benchmark.db` contiene resúmenes pre-agregados usados por el motor de benchmark semanal (actualizado de forma incremental a medida que llegan los eventos).

6. **Benchmark semanal**  
    - En el horario configurado (predeterminado: domingos 02:00 hora local), el daemon activa el motor de benchmark. Una ejecución intra-semana puede activarse manualmente via `F5` en la pestaña **Benchmark Detailed**.
    - El motor lee los agregados desde `benchmark.db`, calcula puntuaciones por modelo (accuracy, latency_p95, tool_rate, cost, quality), las normaliza contra el min/max observado en todos los modelos de la ventana, aplica pesos y calcula el delta vs. la referencia.
    - **Búsqueda del modelo activo**: en el momento de la ejecución, el runner lee `~/.config/opencode/opencode.json` para determinar qué modelo está actualmente configurado para cada agente. La fila de ese modelo recibe `run_status = 'active'`; todos los demás en el mismo ciclo reciben `run_status = 'superseded'`. El nombre completo con prefijo de proveedor se almacena en `raw_model`; las tablas usan el nombre normalizado (sin prefijo).
    - **Reemplazo entre ciclos**: `MarkSupersededRuns()` actualiza las filas `active` previas a `superseded` cuando el modelo activo del agente ha cambiado desde el último ciclo.
    - El descubrimiento de benchmark excluye a los agentes efectivamente solo-error para evitar filas marcadoras como `opencode/unknown` e `INSUFFICIENT_DATA`.
    - Resultado: un veredicto (`KEEP`, `SWITCH` o `INSUFFICIENT_DATA`) por modelo, más una recomendación `active_model` escrita en `~/.metronous/thresholds.json`.

7. **Presentación**  
    - El TUI Dashboard lee `tracking.db` (para flujo de eventos en tiempo real y gráficos de costo diario) y `benchmark.db`/`thresholds.json` (para las pestañas Benchmark History Summary, Benchmark Detailed y Charts).
   - `metronous report` CLI imprime tablas formateadas desde las mismas fuentes.
   - El usuario revisa la recomendación en el dashboard o el reporte CLI y decide manualmente si cambiar el modelo activo en OpenCode.

---

## Detalles del servicio Systemd

- **Ubicación del archivo de servicio**: `~/.config/systemd/user/metronous.service` (creado por `metronous install`)
- **Contenido del archivo de unidad** (simplificado):
  ```ini
  [Unit]
  Description=Metronous Agent Intelligence Daemon
  After=network.target

  [Service]
  Type=simple
  ExecStart=/path/to/metronous server --data-dir /home/user/.metronous/data --daemon-mode
  Restart=on-failure
  RestartSec=5s
  StandardOutput=append:/home/user/.metronous/data/metronous.log
  StandardError=inherit

  [Install]
  WantedBy=default.target
  ```
- **Puntos clave**:
  - Se ejecuta como servicio de **nivel de usuario** (no requiere `sudo`).
  - El flag `--daemon-mode` indica al daemon que use `ServeDaemon()` (solo HTTP, bloquea en contexto, no en stdin).
  - `Restart=on-failure` + `RestartSec=5s` proporciona resiliencia básica ante crashes.
  - Los logs van a `~/.metronous/data/metronous.log` (solo append) y `stderr` (heredado por systemd, visible via `journalctl --user -u metronous`).

---

## Manejo de fallos y resiliencia

| Punto de fallo | Detección | Mitigación |
|----------------|-----------|------------|
| **El shim no puede leer el archivo de puerto** | `readShimPort()` retorna error | El shim entra en `ensureDaemonRunning`: adquiere bloqueo de archivo, verifica nuevamente, inicia el daemon si es necesario. |
| **El archivo de puerto existe pero el daemon está muerto** | La verificación de salud del shim (`GET /health`) falla (conexión rechazada, timeout, no-200) | El shim borra el archivo de puerto obsoleto y procede a iniciar un nuevo daemon via `ensureDaemonRunning`. |
| **El daemon falla al iniciar** (p. ej., binario incorrecto, directorio de datos faltante) | `systemctl --user status metronous` muestra `failed`; el journal muestra error | El usuario debe solucionar el problema subyacente (p. ej., reinstalar el binario, asegurarse de que `~/.metronous/data` exista y sea escribible). Systemd reintentará según `Restart=on-failure`. |
| **Disco lleno o corrupción en SQLite** | El daemon registra error al insertar/agregar; puede retornar HTTP 500 en `/ingest` | Se requiere intervención manual: liberar espacio, restaurar desde backup, o eliminar y dejar que las bases de datos se recreen (perdiendo datos históricos). |
| **La instancia de usuario de systemd no está en ejecución** | Los comandos `systemctl --user` fallan | Asegurarse de que los servicios de usuario systemd estén disponibles en el host. En WSL esto significa habilitar `systemd=true` en `/etc/wsl.conf` y reiniciar WSL. |
| **Problemas de red entre el shim y el daemon** (improbable en localhost) | Las solicitudes HTTP del shim tienen timeout o fallan | El shim trata al daemon como no saludable, borra el archivo de puerto, intenta reinicio. |
| **Demasiadas sesiones de OpenCode** (muchos shims) | Cada shim abre una conexión HTTP al daemon; el servidor HTTP del daemon tiene capacidad concurrente limitada | El `http.Server` del daemon usa `MaxHeaderBytes` predeterminado, etc.; bajo carga extrema puede haber latencia o solicitudes descartadas. Considera aumentar los timeouts HTTP del daemon o usar un proxy inverso si es necesario (no requerido actualmente para uso típico). |

---

## Estructura de directorios

```
~/.metronous/
├── data/
│   ├── tracking.db          # Flujo de eventos sin procesar (modo WAL)
│   ├── benchmark.db         # Resúmenes pre-agregados para benchmarking
│   ├── mcp.port             # Puerto actual del daemon
│   └── metronous.pid        # PID del daemon (si está en ejecución)
└── thresholds.json          # Configuración editable por el usuario: modelo activo, pesos, umbrales, precios de modelos
```

La CLI y el TUI leen/escriben estos archivos directamente cuando es posible; el daemon también los mantiene abiertos mientras se ejecuta.

---

## Extensibilidad

### Agregar una nueva herramienta (p. ej., `report`)
1. Define la estructura de argumentos de la herramienta y la función manejadora en `internal/mcp/` (similar a `ingestHandler`).
2. Registra el manejador en `daemon/service.go` via `mcp.RegisterReportHandler(srv, handler)`.
3. Expone la herramienta en la respuesta `tools/list` del shim (actualmente hardcodeado a solo `ingest`; para hacerlo dinámico, el shim necesitaría obtener `/tools` del daemon — trabajo futuro planificado).
4. Actualiza el plugin de OpenCode para que conozca la nueva herramienta (si quieres que la invoque desde el lado del agente; de lo contrario el daemon puede llamarla internamente basándose en los eventos).

### Agregar una nueva métrica al benchmarking
1. Agrega una columna a la tabla `benchmark_runs` (o una nueva tabla de resumen) en `internal/store/sqlite/benchmark_store.go`.
2. Actualiza la lógica de agregación (`AggregateRun`) para calcular la métrica a partir de eventos sin procesar.
3. Extiende el pipeline de normalización y puntuación en `internal/benchmark/engine.go`:
   - Agrega una función de normalización para la nueva métrica (mayor/menor es mejor).
   - Agrega un peso en `thresholds.json` bajo `weights`.
   - Asegúrate de que la puntuación esté incluida en `final_score`.
4. (Opcional) Agrega un precio o umbral editable por el usuario si la métrica lo requiere (p. ej., el costo por token ya está modelado).

### Cambiar el backend de almacenamiento (p. ej., a Postgres)
- Reemplaza las implementaciones de `internal/store/sqlite/` con versiones que usen `database/sql` y el driver deseado.
- La interfaz (`internal/store/store.go`) ya está abstraída (`EventStore`, `BenchmarkStore`), por lo que el cambio está confinado a las implementaciones de la store.
- Nota: El TUI y la CLI actualmente leen los archivos SQLite directamente; cambiar a Postgres requeriría modificarlos para consultar via HTTP o un socket. Por ahora, SQLite se mantiene por simplicidad y operación local sin configuración.

---

## Diagrama de secuencia (textual)

```
OpenCode Plugin          Shim (metronous mcp)        Daemon (metronous)          SQLite DBs
     |                          |                          |                           |
     |--- MCP tools/call ingest --->|                          |                           |
     |                          |--- HTTP POST /ingest --->|                           |
     |                          |                          |--- HandleIngest --> tracking.db
     |                          |                          |--- upsertAgentSummary --> benchmark.db
     |                          |<--- HTTP 200 OK ---------|                           |
     |<--- MCP tools/call resp ---|                          |                           |
     |                          |                          |                           |
     | (el bucle de eventos continúa) |                   |                           |
```

**Disparador de benchmark (interno al daemon, semanal):**

```
Daemon (internal timer)          Benchmark Engine          SQLite DBs           thresholds.json
     |                              |                           |                           |
     |--- trigger benchmark ------->|--- read aggregates ------->|                           |
     |                              |--- compute scores -------->|                           |
     |                              |--- apply weights ---------->|                           |
     |                              |--- calculate delta -------->|                           |
     |                              |--- decide verdict ---------->|                           |
     |                              |<--- write active_model ----|                           |
     |                              |                           |                           |
     |<--- benchmark complete -----|                           |                           |
```

---

## Notas de cierre

Esta arquitectura ofrece:
- **Instalación en Linux sin fricción** (`curl -fsSL https://github.com/kiosvantra/metronous/releases/latest/download/install.sh | bash` → daemon en ejecución + OpenCode configurado).
- **Daemon local compartido**: las sesiones de OpenCode en la misma máquina se comunican con el mismo daemon via el shim.
- **Observabilidad**: logs, archivos SQLite y estado de systemd proporcionan visibilidad completa de la operación.
- **Simplicidad**: pocas piezas en movimiento, sin dependencias externas más allá de Go y SQLite (ya incluido en el vendor).

Para cualquier pregunta sobre extender o adaptar esta arquitectura, consulta el código fuente o abre una discusión en el repositorio.
