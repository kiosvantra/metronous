> Esta es la traducción al español de tui-controls.md. Para la versión oficial y más actualizada, consulta el archivo en inglés.

# Controles y Navegación del TUI

Metronous ejecuta un panel de terminal de cinco pestañas (TUI):

| # | Pestaña |
|---|---------|
| 1 | Benchmark History Summary |
| 2 | Benchmark Detailed |
| 3 | Tracking |
| 4 | Charts |
| 5 | Config |

## Teclas globales (nivel de la aplicación)
- `q`: salir
- `1`/`2`/`3`/`4`/`5` o `left`/`right`: cambiar pestañas (nota: dentro de **Charts**, `left`/`right` navegan entre meses en lugar de cambiar de pestaña)
- `ctrl+s`: guardar configuración
- `ctrl+r`: recargar configuración

## Pestaña Tracking
- `up`/`down` o `k`/`j`: mover el cursor de sesión (las sesiones se muestran colapsadas)
- `Enter`: abrir el popup de sesión (el contenido del popup se congela al abrirlo)
- `Esc`: cerrar el popup
- Navegación dentro del popup:
  - `up`/`down` o `k`/`j`: moverse dentro del viewport del popup
  - `PgUp`/`PgDn`: desplazar el popup en bloques de 20 filas

## Pestaña Benchmark History Summary

Esta pestaña muestra una vista histórica ponderada de todos los pares (agente, modelo) que han estado activos en los **últimos 4 ciclos semanales**. Está diseñada para monitoreo de salud a simple vista en todos los agentes.

### Diseño de la pantalla

- **Ordenamiento en cascada**: para cada agente, el modelo actualmente activo aparece primero (marcado con `●`). Los modelos históricos que estuvieron activos en ciclos semanales anteriores se listan debajo, ordenados por recencia.
- **Filtro de 4 ciclos**: solo se muestran los pares (agente, modelo) que aparecen en al menos una de las últimas 4 ejecuciones de benchmark semanal. Los modelos más antiguos están ocultos.
- **Columna de veredicto**: el veredicto (`KEEP`, `SWITCH`, `URGENT_SWITCH`, `INSUFFICIENT_DATA`) se muestra **solo** para la fila del modelo activo de cada agente. Las filas de modelos históricos (reemplazados) muestran `—` en la columna de veredicto para evitar comparaciones engañosas.
- **Línea de leyenda**: una leyenda al final de la tabla explica el esquema de ponderación.

### Explicación de la leyenda

```
Weighted historical averages (weekly + intraweek) — showing models active in the last 4 weekly cycles
```

Las métricas mostradas (accuracy, avg response time, cost, health score) son **promedios ponderados** de todas las ejecuciones de benchmark para ese par (agente, modelo). Las ejecuciones semanales recientes reciben mayor peso que las anteriores; las ejecuciones intra-semana contribuyen proporcionalmente a su tamaño de muestra dentro del mismo ciclo.

### Teclas
- `up`/`down` o `k`/`j`: mover el cursor entre filas
- `F5`: ejecutar un benchmark intra-semana de inmediato (igual que el pipeline semanal, pero cubre solo los eventos desde la última ejecución)

## Pestaña Benchmark Detailed
- `up`/`down` o `k`/`j`: seleccionar una fila (una ejecución de agente)
- `PgUp`/`PgDn`: cambiar el ciclo mostrado (semana delimitada por domingo; la navegación va de más reciente a más antigua)
- `Enter`: congelar el panel de detalles para la fila seleccionada
- `Esc`: descongelar el panel de detalles
- `F5`: ejecutar un benchmark intra-semana de inmediato (cubre eventos desde la última ejecución, usando el mismo pipeline que la ejecución semanal)

## Pestaña Charts
- La pestaña Charts muestra un gráfico de costo mensual principal más dos tarjetas de resumen: Performance Top 3 of the Month y Responsibility Top 3 of the Month
- `left`/`right`: cambiar el mes seleccionado
- `k`/`l`: mover el cursor de día dentro del gráfico de costos (actualiza el tooltip)
- Mouse: hover/clic sobre una columna de día para mostrar el tooltip (depende del terminal)
