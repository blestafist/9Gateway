import http from "node:http";
import net from "node:net";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { spawn, ChildProcess } from "node:child_process";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, "../../../");
const binaryPath = path.join(os.tmpdir(), "9gateway-e2e-bin");

export interface GatewayInstance {
  baseURL: string;
  gatewayPort: number;
  upstreamPort: number;
  adminCredential: string;
  authPepper: string;
  sqlitePath: string;
  tmpDir: string;
  proc: ChildProcess;
  upstreamServer: http.Server;
  cleanup: () => Promise<void>;
  createKey: (options: {
    name: string;
    allowedModels?: string[];
    requestBodyLogging?: boolean;
    responseBodyLogging?: boolean;
    budgetMicros?: number;
  }) => Promise<{ id: string; key: string; secret: string }>;
  proxyRequest: (options: {
    keySecret: string;
    model: string;
    messages?: Array<{ role: string; content: string }>;
    stream?: boolean;
  }) => Promise<{ status: number; body: string }>;
}

async function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, "127.0.0.1", () => {
      const address = srv.address() as net.AddressInfo;
      const port = address.port;
      srv.close((err) => {
        if (err) reject(err);
        else resolve(port);
      });
    });
  });
}

export async function ensureGatewayBinary(): Promise<string> {
  // Always build or ensure binary exists
  const buildCmd = spawn("go", ["build", "-o", binaryPath, "./cmd/gateway"], {
    cwd: repoRoot,
    stdio: "pipe",
  });
  await new Promise<void>((resolve, reject) => {
    let stderr = "";
    buildCmd.stderr?.on("data", (d) => (stderr += d.toString()));
    buildCmd.on("exit", (code) => {
      if (code === 0) resolve();
      else reject(new Error(`Failed to build gateway binary (code ${code}): ${stderr}`));
    });
  });
  return binaryPath;
}

export async function startGatewayHarness(): Promise<GatewayInstance> {
  await ensureGatewayBinary();

  const gatewayPort = await getFreePort();
  const upstreamPort = await getFreePort();
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "9gw-e2e-"));
  const sqlitePath = path.join(tmpDir, "gateway.db");
  const adminCredential = "admin-e2e-password-123456";
  const authPepper = "e2e-pepper-98765432101234567890123456";

  // Controlled Upstream
  const upstreamServer = http.createServer((req, res) => {
    if (req.url === "/ready" || req.url === "/healthz") {
      res.writeHead(200, { "Content-Type": "text/plain" });
      res.end("OK");
      return;
    }

    if (req.url === "/v1/chat/completions" && req.method === "POST") {
      let body = "";
      req.on("data", (chunk) => (body += chunk));
      req.on("end", () => {
        let parsed: { model?: string; stream?: boolean } = {};
        try {
          parsed = JSON.parse(body);
        } catch {
          // ignore
        }

        const model = parsed.model || "mock-model";

        if (parsed.stream) {
          res.writeHead(200, {
            "Content-Type": "text/event-stream",
            "Cache-Control": "no-cache",
          });
          res.write(
            `data: ${JSON.stringify({
              id: "chatcmpl-mock-1",
              object: "chat.completion.chunk",
              model,
              choices: [{ index: 0, delta: { role: "assistant", content: "Hello " } }],
            })}\n\n`
          );
          res.write(
            `data: ${JSON.stringify({
              id: "chatcmpl-mock-1",
              object: "chat.completion.chunk",
              model,
              choices: [{ index: 0, delta: { content: "from mock upstream!" }, finish_reason: "stop" }],
            })}\n\n`
          );
          res.write("data: [DONE]\n\n");
          res.end();
          return;
        }

        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(
          JSON.stringify({
            id: "chatcmpl-mock-1",
            object: "chat.completion",
            created: Math.floor(Date.now() / 1000),
            model,
            choices: [
              {
                index: 0,
                message: { role: "assistant", content: "Hello from mock upstream!" },
                finish_reason: "stop",
              },
            ],
            usage: { prompt_tokens: 12, completion_tokens: 8, total_tokens: 20 },
          })
        );
      });
      return;
    }

    res.writeHead(404);
    res.end("Not Found");
  });

  await new Promise<void>((resolve, reject) => {
    upstreamServer.listen(upstreamPort, "127.0.0.1", () => resolve());
    upstreamServer.on("error", reject);
  });

  // Config YAML
  const configLines = [
    `listen_addr: "127.0.0.1:${gatewayPort}"`,
    `upstream_base_url: "http://127.0.0.1:${upstreamPort}"`,
    `upstream_api_key: "upstream-test-key"`,
    `sqlite_path: ${sqlitePath}`,
    `auth_pepper: \${AUTH_PEPPER}`,
    `admin_credential: \${ADMIN_CREDENTIAL}`,
    `tokenizer:`,
    `  mode: estimate`,
    `  max_inspected_request_bytes: 65536`,
    `  fallback_unknown_input_tokens: 4096`,
    `  fallback_max_output_tokens: 4096`,
    `observability:`,
    `  telemetry_queue_capacity: 128`,
    `  max_captured_body_bytes: 262144`,
    `  request_retention_seconds: 2592000`,
    `  body_retention_seconds: 604800`,
    `shutdown_timeout_seconds: 5`,
  ];
  const configPath = path.join(tmpDir, "config.yaml");
  fs.writeFileSync(configPath, configLines.join("\n") + "\n");

  const env = {
    ...process.env,
    AUTH_PEPPER: authPepper,
    ADMIN_CREDENTIAL: adminCredential,
  };

  const proc = spawn(binaryPath, ["--config", configPath], {
    env,
    stdio: "pipe",
  });

  let gatewayStderr = "";
  proc.stderr?.on("data", (d) => (gatewayStderr += d.toString()));

  // Wait for /ready
  let ready = false;
  for (let i = 0; i < 40; i++) {
    await new Promise((r) => setTimeout(r, 100));
    try {
      const code = await new Promise<number>((resolve, reject) => {
        const req = http.get(`http://127.0.0.1:${gatewayPort}/ready`, (res) => {
          resolve(res.statusCode || 0);
        });
        req.on("error", reject);
      });
      if (code === 200) {
        ready = true;
        break;
      }
    } catch {
      // retry
    }
  }

  if (!ready) {
    proc.kill("SIGKILL");
    upstreamServer.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
    throw new Error(`Gateway failed to become ready on port ${gatewayPort}: ${gatewayStderr}`);
  }

  const baseURL = `http://127.0.0.1:${gatewayPort}`;

  const cleanup = async () => {
    try {
      proc.kill("SIGTERM");
      await new Promise<void>((resolve) => {
        const t = setTimeout(() => {
          try {
            proc.kill("SIGKILL");
          } catch {
            // ignore
          }
          resolve();
        }, 3000);
        proc.on("exit", () => {
          clearTimeout(t);
          resolve();
        });
      });
    } catch {
      // ignore
    }

    try {
      await new Promise<void>((resolve) => upstreamServer.close(() => resolve()));
    } catch {
      // ignore
    }

    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      // ignore
    }
  };

  const createKey = async (options: {
    name: string;
    allowedModels?: string[];
    requestBodyLogging?: boolean;
    responseBodyLogging?: boolean;
    budgetMicros?: number;
  }) => {
    const res = await fetch(`${baseURL}/admin/v1/keys`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${adminCredential}`,
      },
      body: JSON.stringify({ name: options.name }),
    });

    if (!res.ok) {
      const text = await res.text();
      throw new Error(`Failed to create key: ${res.status} ${text}`);
    }

    const data = (await res.json()) as { id: string; key: string; secret: string };

    // If custom policy is specified, update it via policy endpoint
    if (
      options.allowedModels ||
      options.requestBodyLogging !== undefined ||
      options.responseBodyLogging !== undefined ||
      options.budgetMicros !== undefined
    ) {
      const policy: Record<string, unknown> = {};
      if (options.allowedModels) {
        policy.allowed_models = options.allowedModels;
      }
      if (options.requestBodyLogging !== undefined) {
        policy.log_request_body = options.requestBodyLogging;
      }
      if (options.responseBodyLogging !== undefined) {
        policy.log_response_body = options.responseBodyLogging;
      }
      if (options.budgetMicros !== undefined) {
        policy.budget_limits = [{ amount_micros: options.budgetMicros, period: "total" }];
      }

      const policyPayload = {
        enabled: true,
        policy,
      };

      const putRes = await fetch(`${baseURL}/admin/v1/keys/${encodeURIComponent(data.id)}/policy`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${adminCredential}`,
        },
        body: JSON.stringify(policyPayload),
      });

      if (!putRes.ok) {
        const text = await putRes.text();
        throw new Error(`Failed to update key policy: ${putRes.status} ${text}`);
      }
    }

    return { ...data, secret: data.key };
  };

  const proxyRequest = async (options: {
    keySecret: string;
    model: string;
    messages?: Array<{ role: string; content: string }>;
    stream?: boolean;
  }) => {
    const payload = {
      model: options.model,
      messages: options.messages || [{ role: "user", content: "Test message from E2E" }],
      stream: !!options.stream,
    };

    const res = await fetch(`${baseURL}/v1/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${options.keySecret}`,
      },
      body: JSON.stringify(payload),
    });

    const body = await res.text();
    return { status: res.status, body };
  };

  return {
    baseURL,
    gatewayPort,
    upstreamPort,
    adminCredential,
    authPepper,
    sqlitePath,
    tmpDir,
    proc,
    upstreamServer,
    cleanup,
    createKey,
    proxyRequest,
  };
}
