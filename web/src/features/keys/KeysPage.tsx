import React, { useState } from "react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
  IconButton,
  Switch,
  Badge,
} from "../../shared/ui";
import { KeyRound, Copy, Check, Eye, EyeOff, Plus } from "lucide-react";

interface KeyItem {
  id: string;
  name: string;
  masked: string;
  full: string;
  created: string;
  enabled: boolean;
}

const SAMPLE_KEYS: KeyItem[] = [
  {
    id: "key-1",
    name: "Main Production Key",
    masked: "sk-c47••••••••2494",
    full: "sk-c47a98b1e42f7c0d2494",
    created: "2026-09-01",
    enabled: true,
  },
  {
    id: "key-2",
    name: "Development Proxy",
    masked: "sk-b16••••••••dcc3",
    full: "sk-b16f39e801ab56eedcc3",
    created: "2026-09-10",
    enabled: true,
  },
  {
    id: "key-3",
    name: "Bot Workflow Key",
    masked: "sk-5ca••••••••9870",
    full: "sk-5ca932147ff0114e9870",
    created: "2026-09-15",
    enabled: false,
  },
];

export const KeysPage: React.FC = () => {
  const [copiedEndpoint, setCopiedEndpoint] = useState(false);
  const [revealedKeys, setRevealedKeys] = useState<Record<string, boolean>>({});
  const [copiedKeyId, setCopiedKeyId] = useState<string | null>(null);

  const endpointUrl = typeof window !== "undefined"
    ? `${window.location.origin}/v1`
    : "http://localhost:8080/v1";

  const handleCopyEndpoint = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(endpointUrl);
      setCopiedEndpoint(true);
      setTimeout(() => setCopiedEndpoint(false), 2000);
    }
  };

  const toggleReveal = (id: string) => {
    setRevealedKeys((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const handleCopyKey = (key: KeyItem) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(revealedKeys[key.id] ? key.full : key.masked);
      setCopiedKeyId(key.id);
      setTimeout(() => setCopiedKeyId(null), 2000);
    }
  };

  return (
    <div className="gw-page-content" data-testid="keys-page">
      {/* Endpoint Card (Hierarchy from 2.png) */}
      <Card className="gw-endpoint-card">
        <CardHeader>
          <div className="gw-endpoint-header">
            <KeyRound size={18} style={{ color: "var(--accent-primary)" }} aria-hidden="true" />
            <CardTitle>API Endpoint</CardTitle>
          </div>
        </CardHeader>
        <CardContent>
          <div className="gw-endpoint-row">
            <span className="gw-endpoint-label">Local</span>
            <code className="gw-endpoint-url">{endpointUrl}</code>
            <IconButton
              icon={copiedEndpoint ? <Check size={16} /> : <Copy size={16} />}
              aria-label={copiedEndpoint ? "Copied endpoint URL" : "Copy endpoint URL"}
              variant="secondary"
              size="sm"
              onClick={handleCopyEndpoint}
            />
          </div>
        </CardContent>
      </Card>

      {/* Keys List Card */}
      <Card>
        <CardHeader>
          <div className="gw-keys-header-row">
            <div>
              <div className="gw-keys-title-line">
                <CardTitle>API Keys</CardTitle>
                <Badge variant="neutral" size="sm">3 keys</Badge>
              </div>
              <p className="gw-card-subtitle">
                Requests without a valid Bearer token will be rejected by gateway policy.
              </p>
            </div>
            <Button
              variant="primary"
              size="sm"
              leftIcon={<Plus size={16} />}
              disabled
              title="Key creation will connect to admin storage in T168"
            >
              Create Key
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <div className="gw-keys-list" role="list">
            {SAMPLE_KEYS.map((key) => {
              const isRevealed = Boolean(revealedKeys[key.id]);
              const isCopied = copiedKeyId === key.id;

              return (
                <div key={key.id} className="gw-key-row" role="listitem">
                  <div className="gw-key-info">
                    <div className="gw-key-name-row">
                      <span className="gw-key-name">{key.name}</span>
                      <span className="gw-key-date">Created {key.created}</span>
                    </div>
                    <div className="gw-key-secret-row">
                      <code className="gw-key-token">
                        {isRevealed ? key.full : key.masked}
                      </code>
                      <IconButton
                        icon={isRevealed ? <EyeOff size={14} /> : <Eye size={14} />}
                        aria-label={isRevealed ? `Mask ${key.name}` : `Reveal ${key.name}`}
                        variant="ghost"
                        size="sm"
                        onClick={() => toggleReveal(key.id)}
                      />
                      <IconButton
                        icon={isCopied ? <Check size={14} /> : <Copy size={14} />}
                        aria-label={isCopied ? `Copied ${key.name}` : `Copy ${key.name}`}
                        variant="ghost"
                        size="sm"
                        onClick={() => handleCopyKey(key)}
                      />
                    </div>
                  </div>
                  <div className="gw-key-actions">
                    <Switch
                      checked={key.enabled}
                      onChange={() => {}}
                      disabled
                      aria-label={`${key.name} status toggle`}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        </CardContent>
      </Card>
    </div>
  );
};

export default KeysPage;
