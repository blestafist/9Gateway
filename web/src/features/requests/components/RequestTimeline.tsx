import React from "react";
import { Clock } from "lucide-react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Badge,
} from "../../../shared/ui";
import {
  formatTimestamp,
  formatDurationMicros,
} from "../../../shared/formatters";
import { AdminRequestDetail } from "../types";

export interface RequestTimelineProps {
  detail: AdminRequestDetail;
}

interface TimelineEvent {
  id: string;
  name: string;
  description: string;
  timestamp: string | null;
  durationMicros: number | null;
  durationLabel?: string;
  status: "completed" | "skipped" | "unrecorded";
}

export const RequestTimeline: React.FC<RequestTimelineProps> = ({ detail }) => {
  const events: TimelineEvent[] = [
    {
      id: "started",
      name: "Request Received",
      description: "Gateway received and admitted client request",
      timestamp: detail.started_at,
      durationMicros: null,
      status: detail.started_at ? "completed" : "unrecorded",
    },
    {
      id: "upstream_started",
      name: "Upstream Dispatched",
      description: "Gateway initiated connection to upstream provider",
      timestamp: detail.upstream_started_at,
      durationMicros: null,
      status: detail.upstream_started
        ? detail.upstream_started_at
          ? "completed"
          : "unrecorded"
        : "skipped",
    },
    {
      id: "upstream_headers",
      name: "Upstream Headers",
      description: "First response headers received from upstream",
      timestamp: detail.upstream_headers_at,
      durationMicros: detail.time_to_upstream_headers_micros,
      durationLabel: "Time to Headers",
      status: detail.upstream_headers_at
        ? "completed"
        : detail.upstream_started
          ? "unrecorded"
          : "skipped",
    },
    {
      id: "first_byte",
      name: "First Byte Delivered",
      description: "First payload chunk sent downstream to client (TTFB)",
      timestamp: detail.first_byte_at,
      durationMicros: detail.time_to_first_byte_micros,
      durationLabel: "TTFB",
      status: detail.first_byte_at
        ? "completed"
        : detail.upstream_started
          ? "unrecorded"
          : "skipped",
    },
    {
      id: "finished",
      name: "Stream / Request Closed",
      description: "Upstream and downstream transport completed and closed",
      timestamp: detail.finished_at,
      durationMicros: detail.stream_close_delay_micros,
      durationLabel: "Stream Close Delay",
      status: detail.finished_at ? "completed" : "unrecorded",
    },
  ];

  return (
    <Card className="gw-request-timeline-card" data-testid="request-timeline">
      <CardHeader>
        <div className="gw-timeline-header-row">
          <div className="gw-timeline-header-title">
            <Clock size={16} aria-hidden="true" />
            <CardTitle>Lifecycle Timeline & Latencies</CardTitle>
          </div>
          <div className="gw-timeline-total-duration">
            <span className="gw-detail-label">Total Duration:</span>
            <Badge variant="neutral" size="sm" data-testid="timeline-total-duration">
              {formatDurationMicros(detail.total_micros)}
            </Badge>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <ol className="gw-timeline-list" aria-label="Request lifecycle events">
          {events.map((event, idx) => {
            const isLast = idx === events.length - 1;

            return (
              <li
                key={event.id}
                className={`gw-timeline-item gw-timeline-status-${event.status}`}
                data-testid={`timeline-event-${event.id}`}
              >
                {/* Visual marker / line */}
                <div className="gw-timeline-marker-container">
                  <div className="gw-timeline-dot" aria-hidden="true" />
                  {!isLast && <div className="gw-timeline-line" aria-hidden="true" />}
                </div>

                {/* Event details */}
                <div className="gw-timeline-content">
                  <div className="gw-timeline-item-header">
                    <div className="gw-timeline-name-group">
                      <span className="gw-timeline-event-name">{event.name}</span>
                      {event.status === "skipped" && (
                        <Badge variant="neutral" size="sm">
                          Skipped
                        </Badge>
                      )}
                    </div>
                    <div className="gw-timeline-timestamp">
                      {event.timestamp ? (
                        <span title={event.timestamp}>
                          {formatTimestamp(event.timestamp)}
                        </span>
                      ) : (
                        <span className="gw-table-dimmed">—</span>
                      )}
                    </div>
                  </div>

                  <p className="gw-timeline-event-desc gw-table-dimmed">
                    {event.description}
                  </p>

                  {/* Associated Latency / Duration Metric */}
                  {event.durationMicros !== null && event.durationMicros !== undefined && (
                    <div className="gw-timeline-metric">
                      <span className="gw-timeline-metric-label">
                        {event.durationLabel || "Duration"}:
                      </span>
                      <code className="gw-timeline-metric-val">
                        {formatDurationMicros(event.durationMicros)}
                      </code>
                    </div>
                  )}
                </div>
              </li>
            );
          })}
        </ol>
      </CardContent>
    </Card>
  );
};
