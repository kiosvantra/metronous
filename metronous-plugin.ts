/**
 * Metronous — OpenCode plugin
 *
 * Tracks agent sessions and tool calls, sends events to the Metronous
 * MCP server via HTTP POST for benchmarking and calibration.
 *
 * Flow:
 *   OpenCode events → this plugin → HTTP POST /ingest → metronous server → SQLite
 *
 * OpenCode spawns the Metronous server as an MCP server (owns stdio for MCP protocol).
 * The plugin connects via HTTP so there is no stdio conflict.
 *
 * Config (env vars):
 *   METRONOUS_AGENT_ID    Agent identifier override (default: derived from chat.params agent field)
 *   METRONOUS_DATA_DIR    Data directory (default: ~/.metronous/data)
 *   METRONOUS_DEBUG       Enable debug logging (default: false)
 *
 * Agent ID resolution (in priority order):
 *   1. METRONOUS_AGENT_ID env var (explicit override)
 *   2. agent field from chat.message / chat.params events (actual OpenCode agent name)
 *   3. "opencode" fallback (platform ID per Gentle AI agent matrix)
 *
 * Tool name: input.tool (string) from tool.execute.after — per @opencode-ai/plugin SDK
 * Model: model.id from chat.params, or providerID/modelID from chat.message
 */

import type { Plugin } from "@opencode-ai/plugin"

// ─── Configuration ────────────────────────────────────────────────────────────

const os = require("os")
// Use explicit METRONOUS_DATA_DIR if set; otherwise default to ~/.metronous/data
// If user provides a path without /data, append it for backward compatibility
const _rawDataDir = process.env.METRONOUS_DATA_DIR
  ? process.env.METRONOUS_DATA_DIR
  : os.homedir() + "/.metronous/data"
const METRONOUS_DATA_DIR = _rawDataDir.endsWith("/data") ? _rawDataDir : _rawDataDir + "/data"

const METRONOUS_DEBUG = process.env.METRONOUS_DEBUG === "true" || process.env.METRONOUS_DEBUG === "1"

// Log to file instead of console to avoid TUI interference
const LOG_FILE = `${METRONOUS_DATA_DIR}/plugin.log`
function writeLog(level: string, ...args: unknown[]) {
  try {
    const fs = require("fs")
    const line = `[${new Date().toISOString()}] [${level}] ${args.map(a => typeof a === "string" ? a : JSON.stringify(a)).join(" ")}\n`
    fs.appendFileSync(LOG_FILE, line)
  } catch {
    // Silent fail - can't log logging errors
  }
}

function log(...args: unknown[]) {
  if (METRONOUS_DEBUG) writeLog("DEBUG", ...args)
}

function logError(...args: unknown[]) {
  writeLog("ERROR", ...args)
}

// ─── Session State ────────────────────────────────────────────────────────────

interface SessionState {
  startTime: number
  model: string
  /** Actual OpenCode agent name (e.g. sdd-apply, sdd-orchestrator) */
  agentId: string
  toolCalls: number
  successfulToolCalls: number
  errors: number
  reworkCount: number
  recentTools: Map<string, number>  // tool_name → last timestamp (ms)
  totalCostUsd: number
  promptTokens: number
  completionTokens: number
  /** MAX cost seen in the current model segment — segment total when idle */
  lastStepCost: number
  /** Last tokens.total seen — used to detect model switches (resets to small value) */
  lastStepTokensTotal: number
  /** Accumulated cost from completed segments (before current segment) */
  completedSegmentsCost: number
  /** The last model actively used in this session (updated on every chat.params) */
  lastActiveModel: string
  /** Timestamp of the last idle event — used to decide when to evict from memory */
  lastIdleAt: number
}

/**
 * normalizeModel preserves the full provider/model string (e.g. "opencode/claude-sonnet-4-6")
 * so that different providers of the same base model can be compared independently.
 * Only cleans up edge cases like empty strings or undefined.
 */
function normalizeModel(model: string): string {
  if (!model || model === "undefined/undefined") return "unknown"
  return model
}

const sessions = new Map<string, SessionState>()

function getOrCreateSession(sessionId: string, agentId = "opencode", model = "unknown"): SessionState {
  if (!sessions.has(sessionId)) {
    const normalizedModel = normalizeModel(model)
    sessions.set(sessionId, {
      startTime: Date.now(),
      model: normalizedModel,
      agentId,
      toolCalls: 0,
      successfulToolCalls: 0,
      errors: 0,
      reworkCount: 0,
      recentTools: new Map(),
      totalCostUsd: 0,
      promptTokens: 0,
      completionTokens: 0,
      lastStepCost: 0,
      lastStepTokensTotal: 0,
      completedSegmentsCost: 0,
      lastActiveModel: normalizedModel,
      lastIdleAt: 0,
    })
  }
  return sessions.get(sessionId)!
}

// ─── Quality Score ────────────────────────────────────────────────────────────

function calculateQualityProxy(state: SessionState): number {
  let score = 1.0

  // Penalize tool failures
  const failureRate = state.toolCalls > 0
    ? (state.toolCalls - state.successfulToolCalls) / state.toolCalls
    : 0
  score -= failureRate * 0.4  // up to -0.4 for all failures

  // Penalize rework (retries)
  if (state.toolCalls > 0) {
    const reworkRate = state.reworkCount / state.toolCalls
    score -= Math.min(reworkRate * 0.2, 0.2)  // up to -0.2
  }

  // Penalize errors
  if (state.errors > 0) {
    score -= Math.min(state.errors * 0.1, 0.3) // up to -0.3
  }

  return Math.max(0, Math.min(1, score)) // clamp 0-1
}

// ─── HTTP Client ──────────────────────────────────────────────────────────────
//
// OpenCode owns the stdio pipe (MCP protocol). The plugin sends telemetry
// events via HTTP POST to /ingest on the server's dynamic port.
// The port is read from {METRONOUS_DATA_DIR}/mcp.port which the server writes
// on startup.

const PORT_FILE = `${METRONOUS_DATA_DIR}/mcp.port`
const MAX_PORT_WAIT_MS = 30_000
const PORT_POLL_INTERVAL_MS = 200
const MAX_PRE_READY_QUEUE = 500

let serverPort: number | null = null
let serverReady = false

// Pre-ready queue: events buffered while waiting for the server to start.
let preReadyQueue: object[] = []

// sleep returns a promise that resolves after ms milliseconds.
function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

// readPortFile attempts to read the dynamic HTTP port from the port file.
// Returns the port number or null if the file does not exist / is unreadable.
function readPortFile(): number | null {
  try {
    const fs = require("fs") as typeof import("fs")
    log(`readPortFile: attempting to read ${PORT_FILE}`)
    const content = fs.readFileSync(PORT_FILE, "utf8").trim()
    const port = parseInt(content, 10)
    if (isNaN(port) || port <= 0 || port > 65535) {
      log(`readPortFile: parsed port is invalid: ${content} -> ${port}`)
      return null
    }
    log(`readPortFile: success, port=${port}`)
    return port
  } catch (err) {
    logError(`readPortFile: failed to read port file: ${(err as Error).message}`)
    return null
  }
}

// waitForServer polls the port file until the server is ready or timeout is reached.
// Once ready, flushes any buffered pre-ready events.
async function waitForServer(agentId: string): Promise<void> {
  log(`Waiting for Metronous server (port file: ${PORT_FILE})`)
  const deadline = Date.now() + MAX_PORT_WAIT_MS
  let attempt = 0

  while (Date.now() < deadline) {
    attempt++
    const port = readPortFile()
    if (port !== null) {
      serverPort = port
      serverReady = true
      log(`Server ready on port ${port} — agent: ${agentId} (found after ${attempt} attempts)`)

      // Flush buffered pre-ready events.
      if (preReadyQueue.length > 0) {
        log(`Flushing ${preReadyQueue.length} buffered pre-ready event(s)`)
        const queued = preReadyQueue.splice(0)
        for (const payload of queued) {
          await httpPost(payload)
        }
      }
      return
    }
    log(`waitForServer: attempt ${attempt}, port file not found yet, retrying in ${PORT_POLL_INTERVAL_MS}ms`)
    await sleep(PORT_POLL_INTERVAL_MS)
  }

  logError(`Metronous server did not start within ${MAX_PORT_WAIT_MS / 1000}s — events will be dropped for this session (tried ${attempt} times)`)
}

// MAX_RECONNECT_ATTEMPTS is how many times httpPost will try to re-read the
// port file and retry after an ECONNREFUSED before giving up on this payload.
const MAX_RECONNECT_ATTEMPTS = 3
const RECONNECT_DELAY_MS = 500

// httpPost sends a JSON payload to POST /ingest on the server.
// On ECONNREFUSED it re-reads mcp.port (the daemon may have restarted and
// changed ports) and retries up to MAX_RECONNECT_ATTEMPTS times.
async function httpPost(payload: object): Promise<void> {
  if (!serverPort) {
    log("httpPost: serverPort is null, dropping event")
    return
  }
  const http = require("http") as typeof import("http")
  const body = JSON.stringify(payload)

  for (let attempt = 0; attempt <= MAX_RECONNECT_ATTEMPTS; attempt++) {
    const port = serverPort!
    const success = await new Promise<boolean>((resolve) => {
      const req = http.request(
        {
          hostname: "127.0.0.1",
          port,
          path: "/ingest",
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "Content-Length": Buffer.byteLength(body),
          },
        },
        (res) => {
          // Drain the response body so Node.js doesn't leak the socket.
          res.resume()
          res.on("end", () => resolve(true))
        }
      )
      req.setTimeout(5000, () => {
        req.destroy()
        resolve(false)
      })
      req.on("error", (err: Error & { code?: string }) => {
        if (err.code === "ECONNREFUSED") {
          // Daemon restarted — invalidate cached port so we re-read the file.
          log(`httpPost: ECONNREFUSED on port ${port} — will re-read mcp.port (attempt ${attempt + 1}/${MAX_RECONNECT_ATTEMPTS})`)
          serverPort = null
          serverReady = false
        } else {
          logError("HTTP ingest error:", err.message)
        }
        resolve(false)
      })
      req.end(body)
    })

    if (success) return

    if (attempt < MAX_RECONNECT_ATTEMPTS) {
      // Wait briefly then re-read the port file.
      await sleep(RECONNECT_DELAY_MS)
      const newPort = readPortFile()
      if (newPort !== null) {
        serverPort = newPort
        serverReady = true
        log(`httpPost: reconnected to daemon on new port ${newPort}`)
      } else {
        log("httpPost: mcp.port not available yet, will retry")
      }
    } else {
      logError(`httpPost: giving up after ${MAX_RECONNECT_ATTEMPTS} reconnect attempts`)
    }
  }
}

async function callIngest(payload: object): Promise<void> {
  const eventType = (payload as { event_type?: string }).event_type

  if (!serverReady) {
    // Daemon may have restarted — try to re-read the port file before buffering.
    const port = readPortFile()
    if (port !== null) {
      serverPort = port
      serverReady = true
      log(`callIngest: daemon recovered on port ${port}, flushing pre-ready queue`)
      // Flush any buffered events first.
      if (preReadyQueue.length > 0) {
        const queued = preReadyQueue.splice(0)
        for (const buffered of queued) {
          await httpPost(buffered)
        }
      }
    } else {
      // Still not ready — buffer the event.
      if (preReadyQueue.length >= MAX_PRE_READY_QUEUE) {
        preReadyQueue.shift() // drop oldest to bound memory
        writeLog("WARN", "[Metronous] preReadyQueue full, dropped oldest event")
      }
      log(`Not ready yet, buffering ${eventType}`)
      preReadyQueue.push(payload)
      return
    }
  }

  log(`Sending ingest via HTTP: ${eventType}`)
  await httpPost(payload)
}

// ─── Plugin ───────────────────────────────────────────────────────────────────

export const plugin: Plugin = async ({ directory, client }) => {
  // Derive agent ID — resolution order:
  //   1. METRONOUS_AGENT_ID env var (explicit override)
  //   2. agent field from chat.message / chat.params (actual OpenCode agent name)
  //   3. "opencode" fallback (platform ID per Gentle AI agent matrix)
  const envAgentId = process.env.METRONOUS_AGENT_ID || null

  // This is the "current" agentId — updated by chat.message/chat.params as sessions resolve
  // to real agent names. Used as default for sessions that haven't seen a chat event yet.
  let currentAgentId = envAgentId ?? "opencode"

  log(`Initializing — default agent: ${currentAgentId}, data: ${METRONOUS_DATA_DIR}`)

  // Wait for the server OpenCode spawned (non-blocking — if it doesn't start,
  // events are silently dropped after timeout).
  waitForServer(currentAgentId).catch((err: Error) => {
    logError("waitForServer error:", err.message)
  })

  const now = () => new Date().toISOString()

  return {
    // ── chat.message: fired when a new message is received
    // Provides: sessionID, agent, model, AND parts: Part[] which includes StepFinishPart with cost/tokens
    "chat.message": async (input, output) => {
      try {
        const sessionId = input.sessionID
        if (!sessionId) return

        // Resolve agent name — input.agent may be a string or an object
        const rawAgent = input.agent as any
        const resolvedAgent = typeof rawAgent === "string"
          ? rawAgent
          : rawAgent?.name ?? rawAgent?.id ?? (rawAgent ? JSON.stringify(rawAgent) : null)
        const agentName = envAgentId ?? resolvedAgent ?? "opencode"

        // Build model string and normalize to strip provider prefixes.
        const rawModelStr = input.model
          ? `${input.model.providerID}/${input.model.modelID}`
          : "unknown"
        const modelStr = normalizeModel(rawModelStr)

        // Update current agent for sessions not yet seen
        if (!envAgentId && resolvedAgent) {
          currentAgentId = resolvedAgent
        }

        const isNewSession = !sessions.has(sessionId)
        const state = getOrCreateSession(sessionId, agentName, modelStr)
        // Update model/agent if the session already existed with defaults.
        if (state.model === "unknown" && modelStr !== "unknown") state.model = modelStr
        if (state.agentId === "opencode" && agentName !== "opencode") state.agentId = agentName
        // Always track the last active model (may change mid-session).
        // Only update if the new model has a provider prefix — never downgrade to a bare model name.
        if (modelStr !== "unknown" && modelStr.includes("/")) state.lastActiveModel = modelStr

        log(`chat.message — session: ${sessionId}, agent: ${agentName}, model: ${modelStr}`)

        if (isNewSession) {
          await callIngest({
            agent_id: agentName,
            event_type: "start",
            session_id: sessionId,
            model: modelStr,
            timestamp: now(),
          })
        }
      } catch (err) {
        logError("chat.message error:", err)
      }
    },

    // ── chat.params: fired before each LLM call — most reliable source of agent + model
    "chat.params": async (input, _output) => {
      try {
        const sessionId = input.sessionID
        if (!sessionId) return

        // input.agent may be a string or an object — extract the name safely
        const rawAgent = input.agent as any
        const resolvedAgent = typeof rawAgent === "string"
          ? rawAgent
          : rawAgent?.name ?? rawAgent?.id ?? (rawAgent ? JSON.stringify(rawAgent) : null)
        const agentName = envAgentId ?? resolvedAgent ?? "opencode"

        // Model may have .id (short: "claude-sonnet-4-6") and also .providerID/.modelID
        // Prefer building the full "providerID/modelID" string; fall back to .id
        const rawModel = input.model as any
        const rawModelStr = rawModel
          ? (rawModel.providerID && rawModel.modelID
              ? `${rawModel.providerID}/${rawModel.modelID}`
              : rawModel.id ?? "unknown")
          : "unknown"
        const modelStr = normalizeModel(rawModelStr)

        if (!envAgentId && resolvedAgent) {
          currentAgentId = resolvedAgent
        }

        const state = getOrCreateSession(sessionId, agentName, modelStr)
        if (state.model === "unknown" && modelStr !== "unknown") state.model = modelStr
        if (state.agentId === "opencode" && agentName !== "opencode") state.agentId = agentName
        // Always update lastActiveModel — chat.params fires before every LLM call.
        // Only update if the new model has a provider prefix — never downgrade to a bare model name.
        if (modelStr !== "unknown" && modelStr.includes("/")) state.lastActiveModel = modelStr

        // Debug: log raw agent/model shapes to help diagnose future issues
        log("CHAT_PARAMS", JSON.stringify({ rawAgent, rawModel, agentName, modelStr }))
        log(`chat.params — session: ${sessionId}, agent: ${agentName}, model: ${modelStr}`)
      } catch (err) {
        logError("chat.params error:", err)
      }
    },

    // ── tool.execute.after: fired after every tool call
    // SDK signature: (input: { tool: string; sessionID: string; callID: string; args: any },
    //                 output: { title: string; output: string; metadata: any })
    // IMPORTANT: tool name is in `input.tool` (a plain string) — NOT toolName/tool_name/name
    "tool.execute.after": async (input, output) => {
      try {
        const sessionId = input.sessionID
        if (!sessionId) return

        // `input.tool` is the tool name per the @opencode-ai/plugin SDK type definition
        const toolName = (input.tool || "unknown") as string

        const success = !(output as any)?.error && !(input as any)?.error
        const state = getOrCreateSession(sessionId, currentAgentId)
        state.toolCalls++
        if (success) state.successfulToolCalls++
        else state.errors++

        // Rework detection: same tool called again within 60 seconds
        const lastCall = state.recentTools.get(toolName) ?? 0
        const nowMs = Date.now()
        if (nowMs - lastCall < 60000 && lastCall > 0) {
          state.reworkCount++
        }
        state.recentTools.set(toolName, nowMs)

        log("RAW_TOOL", JSON.stringify({ tool: toolName, metadata: (output as any)?.metadata, inputKeys: Object.keys(input as any) }))
        log(`Tool: ${toolName} — ${success ? "✓" : "✗"} (agent: ${state.agentId})`)

        await callIngest({
          agent_id: state.agentId,
          event_type: "tool_call",
          session_id: sessionId,
          model: state.model,
          tool_name: toolName,
          tool_success: success,
          duration_ms: (output?.metadata as any)?.durationMs ?? 0,
          cost_usd: state.totalCostUsd,
          prompt_tokens: state.promptTokens,
          completion_tokens: state.completionTokens,
          rework_count: state.reworkCount,
          timestamp: now(),
        })
      } catch (err) {
        logError("tool.execute.after error:", err)
      }
    },



    // ── event: real-time hook for all OpenCode events
    // Handles: message.part.updated (step-finish cost/tokens), session.idle, session.error
    "event": async ({ event }: { event: any }) => {
      try {
        if (event.type === "message.part.updated") {
          const part = event.properties?.part
          if (!part || part.type !== "step-finish") return
          const sessionId = part.sessionID
          if (!sessionId) return
          const state = sessions.get(sessionId)
          if (!state) return
          // step-finish.cost fluctuates per turn (cache hits change the per-turn price).
          // tokens.total is cumulative PER MODEL SEGMENT and resets to a small value on model switch.
          // Strategy: track max(cost) per segment. On model switch (tokens.total drops), 
          // commit the previous segment's max to completedSegmentsCost and start fresh.
          const newCost = part.cost ?? 0
          const newTokensTotal = part.tokens?.total ?? 0
          if (newTokensTotal < state.lastStepTokensTotal) {
            // tokens.total dropped → model switched → commit previous segment max
            state.completedSegmentsCost += state.lastStepCost
            state.lastStepCost = newCost
          } else {
            // same segment — track max cost seen in this segment
            if (newCost > state.lastStepCost) {
              state.lastStepCost = newCost
            }
          }
          state.lastStepTokensTotal = newTokensTotal
          // totalCostUsd = completed segments + current segment max
          state.totalCostUsd = state.completedSegmentsCost + state.lastStepCost
          state.promptTokens = newTokensTotal
          state.completionTokens += part.tokens?.output ?? 0
          log(`step-finish — cost=$${newCost.toFixed(4)} segMax=$${state.lastStepCost.toFixed(4)} completed=$${state.completedSegmentsCost.toFixed(4)} total=$${state.totalCostUsd.toFixed(4)}`)
        }

        if (event.type === "session.idle") {
          const sessionId = event.properties?.sessionID
          if (!sessionId) return
          const state = sessions.get(sessionId)
          if (!state) return

          // Snapshot duration BEFORE any await to avoid wall-clock inflation.
          const durationMs = Date.now() - state.startTime

          // Reconcile via client.session.messages() using delta logic:
          // Reconcile using segment-max strategy:
          // tokens.total resets on model switch → detect segments, sum MAX(cost) per segment.
          try {
            const result = await client.session.messages({ path: { id: sessionId } })
            writeLog("RAW_MESSAGES", JSON.stringify(result).slice(0, 500))
            const messages = (result as any)?.data ?? []
            let completedCost = 0, segMaxCost = 0, lastTokensTotal = 0
            let completionSum = 0, promptTotal = 0
            for (const msg of messages) {
              for (const part of (msg.parts ?? [])) {
                if (part?.type === "step-finish") {
                  const c = part.cost ?? 0
                  const t = part.tokens?.total ?? 0
                  if (t < lastTokensTotal) {
                    // model switch — commit previous segment max
                    completedCost += segMaxCost
                    segMaxCost = c
                  } else if (c > segMaxCost) {
                    segMaxCost = c
                  }
                  lastTokensTotal = t
                  completionSum += part.tokens?.output ?? 0
                  promptTotal = t > 0 ? t : promptTotal
                }
              }
            }
            const costTotal = completedCost + segMaxCost
            if (costTotal > 0 || promptTotal > 0) {
              state.totalCostUsd = costTotal
              state.promptTokens = promptTotal
              state.completionTokens = completionSum
            }
            log(`Session idle reconciled — cost: $${state.totalCostUsd.toFixed(4)}, tokens: ${state.promptTokens}/${state.completionTokens}, model: ${state.lastActiveModel}`)
          } catch (e) {
            log(`Could not reconcile messages: ${e}`)
          }

          const quality = calculateQualityProxy(state)
          // Use lastActiveModel (the model active at session end) for the complete event.
          const completeModel = state.lastActiveModel !== "unknown"
            ? state.lastActiveModel
            : state.model
          await callIngest({
            agent_id: state.agentId,
            event_type: "complete",
            session_id: sessionId,
            model: completeModel,
            cost_usd: state.totalCostUsd,
            prompt_tokens: state.promptTokens,
            completion_tokens: state.completionTokens,
            quality_score: quality,
            rework_count: state.reworkCount,
            duration_ms: durationMs,
            timestamp: now(),
          })

          // Keep the session in memory indefinitely.
          // The user may return to the same session after minutes, hours, or days.
          // The map is reset naturally when OpenCode restarts — no manual eviction needed.
          state.lastIdleAt = Date.now()
        }

        if (event.type === "session.error") {
          const sessionId = event.properties?.sessionID
          if (!sessionId) return
          const state = sessions.get(sessionId)
          if (state) state.errors++
          const resolvedAgentId = state?.agentId ?? currentAgentId
          await callIngest({
            agent_id: resolvedAgentId,
            event_type: "error",
            session_id: sessionId,
            model: state?.model ?? "unknown",
            timestamp: now(),
          })
        }
      } catch (err) {
        logError("event hook error:", err)
      }
    },
  }
}

export default plugin
