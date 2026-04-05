<p align="center">
  <img src="assets/logo.png" alt="Metronous" width="100%"/>
</p>

> Esta es la traducción al español de README.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Metronous

> Telemetría local, benchmarking y calibración de modelos para agentes OpenCode.

*Desarrollado originalmente dentro del ecosistema Gentle AI.*

Metronous registra cada llamada a herramientas, sesión y costo de tus agentes OpenCode — luego ejecuta benchmarks semanales para indicarte qué agentes tienen bajo rendimiento y qué modelo te ahorraría dinero.

## Qué hace

- **Registra** sesiones de agentes, llamadas a herramientas, tokens y costos en tiempo real
- **Evalúa** cada agente contra umbrales de precisión y ROI
- **Recomienda** cambios de modelo con razonamiento basado en datos
- **Visualiza** todo en un panel de terminal de 5 pestañas (TUI)

## Arquitectura

```
OpenCode → metronous-plugin.ts → HTTP POST /ingest → metronous daemon → SQLite
                                                               ↓
                                                    metronous dashboard (TUI)
```

- **Plugin (`metronous-plugin.ts`)**: Plugin de OpenCode que captura eventos de agentes y los reenvía al daemon via HTTP. Acumula el costo a partir de eventos `step-finish` y persiste el costo de la sesión en `~/.metronous/data/session_costs.json` entre reinicios.
- **MCP shim (`metronous mcp`)**: Puente stdio↔HTTP iniciado por OpenCode como servidor MCP. Lee el puerto del daemon desde `~/.metronous/data/mcp.port` y reenvía los eventos.
- **Daemon (`metronous server --daemon-mode`)**: Servicio de fondo de larga duración (systemd en Linux) que ingesta eventos, los almacena en SQLite y ejecuta benchmarks semanales los domingos a las 02:00 hora local.
- **TUI Dashboard**: Interfaz de terminal de 5 pestañas con seguimiento en vivo, resultados de benchmark, gráficos de costos y edición de configuración.

Para detalles completos de los componentes consulta [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).  
Para la metodología de benchmarks consulta [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Requisitos previos

1. **[OpenCode](https://opencode.ai) instalado** — `curl -fsSL https://opencode.ai/install | bash`

Metronous funciona con los agentes integrados de OpenCode sin configuración adicional. Si tienes un archivo `opencode.json`, Metronous lo modificará automáticamente. Si no lo tienes, lo creará y podrás agregar proveedores y agentes más adelante.

Go 1.22+ solo es necesario para compilaciones desde el código fuente y `go install`.

## Instalación

### Matriz de compatibilidad

- **Linux**: flujo de instalación oficial (un solo comando)
- **macOS**: flujo de instalación oficial (un solo comando)
- **Windows**: experimental/manual

### Linux (recomendado — un solo comando)

```bash
curl -fsSL https://github.com/kiosvantra/metronous/releases/latest/download/install.sh | bash
```

Este script descarga la última versión, verifica el checksum, instala el binario en `~/.local/bin` y ejecuta `metronous install` para configurar el servicio systemd y OpenCode automáticamente.

> No ejecutes con `sudo`. Debe ejecutarse con el mismo usuario normal que usa OpenCode.

### Linux (manual)

```bash
VERSION=$(curl -sSL https://api.github.com/repos/kiosvantra/metronous/releases/latest | grep '"tag_name"' | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
TARBALL="metronous_${VERSION#v}_linux_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/kiosvantra/metronous/releases/download/${VERSION}/${TARBALL}"
curl -fsSLO "https://github.com/kiosvantra/metronous/releases/download/${VERSION}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "${TARBALL}"
mkdir -p ~/.local/bin
install -m 0755 ./metronous ~/.local/bin/metronous
rm -f "${TARBALL}" checksums.txt
~/.local/bin/metronous install
```

### Via Go (Linux, con servicios de usuario systemd)

```bash
go install github.com/kiosvantra/metronous/cmd/metronous@latest
# Asegurate de que el binario instalado esté en tu PATH, luego ejecuta:
metronous install
```

### Compilación manual desde el código fuente

```bash
git clone https://github.com/kiosvantra/metronous
cd metronous
go build -o metronous ./cmd/metronous
```

Linux:

```bash
mkdir -p ~/.local/bin
install -m 0755 ./metronous ~/.local/bin/metronous
~/.local/bin/metronous install
```

### Flujo de prueba manual en Windows (experimental)

```powershell
# Descarga el archivo de Windows correspondiente desde GitHub Releases,
# por ejemplo: metronous_<version>_windows_amd64.zip
# Ejecuta PowerShell como Administrador antes de continuar.
$archive = "metronous_<version>_windows_amd64.zip"
Expand-Archive -Path $archive -DestinationPath .\metronous-release -Force
$exe = Get-ChildItem .\metronous-release -Recurse -Filter metronous.exe | Select-Object -First 1
$dest = "$env:LOCALAPPDATA\Programs\Metronous"
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Move-Item $exe.FullName "$dest\metronous.exe" -Force
& "$dest\metronous.exe" install
```

Opcionalmente, verifica el archivo antes de extraerlo comparando su hash SHA-256 con `checksums.txt` de la misma versión.

Ejecuta la sesión elevada de PowerShell con la misma cuenta de usuario de Windows que usa OpenCode.

El soporte para Windows es actualmente experimental. El flujo de instalación/servicio nativo aún se está consolidando, por lo que Linux es el único instalador oficialmente soportado.

### macOS (un solo comando)

```bash
curl -fsSL https://github.com/kiosvantra/metronous/releases/latest/download/install.sh | bash
```

Igual que Linux — descarga la última versión, verifica el checksum, instala el binario en `~/.local/bin` y ejecuta `metronous install` para configurar el daemon y OpenCode automáticamente.

Compatible con Intel (amd64) y Apple Silicon (arm64).

> No ejecutes con `sudo`. Debe ejecutarse con el mismo usuario normal que usa OpenCode.

### Notas sobre el servicio en Windows

```powershell
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" install
```

> **Nota:** `metronous install` en Windows requiere una terminal elevada (Ejecutar como Administrador) para registrar el servicio de Windows.

Para control manual:
```powershell
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service start
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service stop
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service status
& "$env:LOCALAPPDATA\Programs\Metronous\metronous.exe" service uninstall
```

### Configurar OpenCode (realizado automáticamente por `metronous install` en Linux)

Después de ejecutar `metronous install` en Linux, OpenCode quedará configurado con:

1. **MCP shim**: la ruta del ejecutable instalado más `mcp` para la ingestión de telemetría
2. **Plugin de OpenCode**: `metronous.ts` copiado en `~/.config/opencode/plugins/`

El plugin captura sesiones de agentes y reenvía eventos al daemon via HTTP.

Luego reinicia OpenCode y mostrará **"Metronous Connected"**.

## Uso

### Dashboard

```bash
metronous dashboard
```

El dashboard tiene cinco pestañas (presiona el número correspondiente para cambiar):

| # | Pestaña | Descripción |
|---|---------|-------------|
| 1 | **Benchmark History Summary** | Vista histórica ponderada de todos los pares (agente, modelo) activos en los últimos 4 ciclos semanales. Ordenamiento en cascada: modelo activo primero (marcado con `●`), modelos anteriores debajo. El veredicto solo se muestra para el modelo activo. |
| 2 | **Benchmark Detailed** | Historial por ejecución agrupado en ciclos delimitados por domingo. Presiona `Enter` para congelar/descongelar el panel de detalles. PgUp/PgDn para navegar entre ciclos. Presiona `F5` para ejecutar un benchmark intra-semana. |
| 3 | **Tracking** | Flujo de sesiones en tiempo real (últimas 20 sesiones, se actualiza cada 2s). Presiona `Enter` sobre una sesión para abrir el popup de Session Timeline con el desglose de costo por evento. |
| 4 | **Charts** | Gráfico de costo mensual por modelo (escala logarítmica, barras apiladas) con tooltip por día. También muestra las tarjetas de los 3 mejores en Performance y Responsibility. `←`/`→` para navegar meses, `k`/`l` o mouse para mover el cursor de día. |
| 5 | **Config** | Editar umbrales de rendimiento. Los cambios se guardan en `~/.metronous/thresholds.json` y se propagan en vivo al motor de benchmarks. |

Para navegación con teclado consulta [docs/tui-controls.md](docs/tui-controls.md).

### Popup Session Timeline (pestaña Tracking)

Presiona `Enter` sobre cualquier fila de sesión en la pestaña Tracking para abrir un popup con el timeline completo de eventos de esa sesión. El popup muestra:

| Columna | Significado |
|---------|-------------|
| `Spent(acc)` | Costo acumulado hasta este evento (instantánea acumulativa almacenada en la base de datos) |
| `Spent(step)` | Delta entre eventos consecutivos = costo por llamada LLM (coincide con la facturación del proveedor) |

`Spent(step)` es la columna más útil para validar costos: cada entrada corresponde a una solicitud LLM y su valor debería coincidir con lo que cobra el proveedor por cada llamada.

### Seguimiento de costos del plugin

El plugin calcula el costo de sesión **sumando** el campo `cost` de cada evento `step-finish` emitido por el hook `message.part.updated` de OpenCode. Esto proporciona el costo real por solicitud que se acumula durante la sesión.

Comportamientos clave:
- El costo se persiste en `~/.metronous/data/session_costs.json` para que los reinicios en medio de una sesión no pierdan el costo acumulado.
- `lastActiveModel` solo se actualiza cuando la cadena del modelo tiene un prefijo de proveedor (p. ej., `opencode/claude-sonnet-4-6`). Los nombres de modelo sin prefijo como `claude-sonnet-4-6` nunca se usan para reemplazar un modelo con prefijo de proveedor ya conocido.
- Los valores NaN y no finitos en los campos de costo y tokens se descartan silenciosamente.

### Benchmark manual

```bash
# Via TUI: presiona F5 en la pestaña Benchmark Detailed
# Via CLI:
METRONOUS_DATA_DIR=~/.metronous/data go run cmd/run-benchmark/main.go
```

## Directorio de datos

Todos los datos se almacenan en `~/.metronous/`:

```
~/.metronous/
├── data/
│   ├── tracking.db          # Telemetría de eventos (SQLite, modo WAL)
│   ├── benchmark.db         # Historial de ejecuciones de benchmark (SQLite)
│   ├── mcp.port             # Puerto HTTP dinámico (tiempo de ejecución)
│   ├── metronous.pid        # PID del servidor (tiempo de ejecución)
│   ├── session_costs.json   # Costos de sesión persistidos entre reinicios del plugin
│   └── plugin.log           # Log de depuración del plugin (cuando METRONOUS_DEBUG=true)
└── thresholds.json          # Umbrales de rendimiento (editables via TUI)
```

## Umbrales de configuración

La pestaña Config (`5`) expone tres campos activos:

| Campo | Predeterminado | Efecto |
|-------|----------------|--------|
| **Min Accuracy** | 0.85 | Precisión por debajo de este valor activa `SWITCH` |
| **Min ROI Score** | 0.05 | ROI por debajo de este valor activa `SWITCH` (solo modelos de pago) |
| **Max Cost/Session** | $0.50 | Referencia para el color del semáforo de costo en la pestaña Tracking; también se usa para la detección de picos urgentes |

ROI = `accuracy / avg_cost_per_session`. Un ROI mayor significa más salida precisa por dólar gastado.

Para el esquema completo de umbrales y disparadores urgentes consulta [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Metodología de benchmarks

- **Accuracy** = `(total_events - error_events) / total_events`
- **ROI** = `accuracy / cost_per_session` donde cost_per_session = `suma de MAX(cost_usd) por session_id`
- **Health score** = 60 pts precisión + 25 pts veredicto + 15 pts ROI (escala 0–100)
- **Veredictos**: `KEEP` / `SWITCH` / `URGENT_SWITCH` / `INSUFFICIENT_DATA`
- Tamaño mínimo de muestra: **50 eventos** (por debajo de esto → `INSUFFICIENT_DATA`)
- `URGENT_SWITCH` se activa cuando la precisión es < 0.60 o la tasa de errores > 30%

Para detalles completos de la metodología consulta [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Agentes rastreados

Metronous descubre automáticamente todos los agentes a partir de los eventos en la base de datos de seguimiento:

- **Agentes integrados**: `build`, `plan`, `general`, `explore`
- **Agentes personalizados**: cualquier agente definido en `opencode.json` o `~/.config/opencode/agents/*.md`

Para el benchmarking, cada agente se evalúa de forma independiente por modelo utilizado. Aquí hay un conjunto de ejemplo del ecosistema Gentle AI SDD:

| Agente | Rol |
|--------|-----|
| `sdd-orchestrator` | Coordina sub-agentes, nunca trabaja de forma directa |
| `sdd-apply` | Implementa cambios de código a partir de definiciones de tareas |
| `sdd-explore` | Investiga el código fuente y analiza ideas |
| `sdd-verify` | Valida la implementación contra las especificaciones |
| `sdd-spec` | Escribe especificaciones detalladas a partir de propuestas |
| `sdd-design` | Crea diseño técnico a partir de propuestas |
| `sdd-propose` | Crea propuestas de cambio a partir de exploraciones |
| `sdd-tasks` | Desglosa especificaciones y diseños en tareas |
| `sdd-init` | Inicializa el contexto SDD y la configuración del proyecto |
| `sdd-archive` | Archiva artefactos de cambios completados |

## Licencia

MIT — ver [LICENSE](LICENSE)
