import React from "react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  StatusPill,
} from "../../shared/ui";
import { Server, Database, Shield, Cpu } from "lucide-react";

export const SystemPage: React.FC = () => {
  return (
    <div className="gw-page-content" data-testid="system-page">
      {/* Service Health Overview */}
      <div className="gw-system-grid">
        <Card>
          <CardHeader>
            <div className="gw-system-card-title">
              <Server size={18} style={{ color: "var(--accent-primary)" }} aria-hidden="true" />
              <CardTitle>Gateway Core Health</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Service Status</span>
                <StatusPill label="Healthy" />
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Upstream 9router</span>
                <StatusPill label="Connected" />
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Readiness Probe</span>
                <span className="gw-table-dimmed">HTTP 200 OK (/ready)</span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="gw-system-card-title">
              <Database size={18} style={{ color: "var(--metric-tokens-cache)" }} aria-hidden="true" />
              <CardTitle>Storage & Persistence</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">SQLite Storage</span>
                <StatusPill label="WAL Active" />
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Schema Version</span>
                <code className="gw-code-id">v9</code>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Retention Policy</span>
                <span className="gw-table-dimmed">Bounded retention active</span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="gw-system-card-title">
              <Shield size={18} style={{ color: "var(--status-success)" }} aria-hidden="true" />
              <CardTitle>Security & Policies</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Secret Redaction</span>
                <span className="gw-status-active">Enforced</span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Body Capture</span>
                <span className="gw-table-dimmed">Selective policy</span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Session Boundary</span>
                <span className="gw-table-dimmed">HttpOnly SameSite=Strict</span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="gw-system-card-title">
              <Cpu size={18} style={{ color: "var(--metric-cost)" }} aria-hidden="true" />
              <CardTitle>Build & Environment</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            <div className="gw-system-kv-list">
              <div className="gw-system-kv">
                <span className="gw-system-k">Version</span>
                <code className="gw-code-id">v0.1.0-rc1</code>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Go Runtime</span>
                <span className="gw-table-dimmed">go1.24 (linux/amd64)</span>
              </div>
              <div className="gw-system-kv">
                <span className="gw-system-k">Web UI Assets</span>
                <span className="gw-table-dimmed">Go embedded static</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export default SystemPage;
