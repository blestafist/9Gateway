import React, { useState } from "react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  StatusPill,
  Input,
  Select,
} from "../../shared/ui";
import { Search } from "lucide-react";

interface RequestSample {
  id: string;
  time: string;
  model: string;
  status: "healthy" | "warning" | "danger";
  statusCode: number;
  tokensIn: number;
  tokensOut: number;
  latencyMs: number;
}

const SAMPLE_REQUESTS: RequestSample[] = [
  {
    id: "req_01j7x8a",
    time: "2m ago",
    model: "claude-sonnet-5",
    status: "healthy",
    statusCode: 200,
    tokensIn: 29609,
    tokensOut: 135,
    latencyMs: 382,
  },
  {
    id: "req_01j7x7b",
    time: "4m ago",
    model: "claude-sonnet-5",
    status: "healthy",
    statusCode: 200,
    tokensIn: 29173,
    tokensOut: 199,
    latencyMs: 412,
  },
  {
    id: "req_01j7x6c",
    time: "12m ago",
    model: "gpt-luna",
    status: "healthy",
    statusCode: 200,
    tokensIn: 4712,
    tokensOut: 84,
    latencyMs: 245,
  },
  {
    id: "req_01j7x5d",
    time: "18m ago",
    model: "gemini-3.8-flash",
    status: "warning",
    statusCode: 429,
    tokensIn: 1204,
    tokensOut: 0,
    latencyMs: 45,
  },
  {
    id: "req_01j7x4e",
    time: "25m ago",
    model: "claude-sonnet-5",
    status: "healthy",
    statusCode: 200,
    tokensIn: 49670,
    tokensOut: 257,
    latencyMs: 512,
  },
];

export const RequestsPage: React.FC = () => {
  const [filterText, setFilterText] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");

  return (
    <div className="gw-page-content" data-testid="requests-page">
      {/* Search and Filters Bar */}
      <div className="gw-requests-filters">
        <div className="gw-requests-search">
          <Input
            placeholder="Search by ID, model, or path..."
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
            leftIcon={<Search size={16} />}
            aria-label="Filter requests"
          />
        </div>
        <div className="gw-requests-filter-select">
          <Select
            options={[
              { value: "all", label: "All Statuses" },
              { value: "200", label: "200 OK" },
              { value: "4xx", label: "4xx Client Error" },
              { value: "5xx", label: "5xx Server Error" },
            ]}
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            aria-label="Filter by status"
          />
        </div>
      </div>

      {/* Requests Table Card */}
      <Card>
        <CardHeader>
          <div className="gw-requests-header-line">
            <CardTitle>Recent Request Traces</CardTitle>
            <span className="gw-card-subtitle">
              Pagination and live query filters will connect in T173/T174.
            </span>
          </div>
        </CardHeader>
        <CardContent>
          <Table aria-label="Recent Requests">
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>Request ID</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Status</TableHead>
                <TableHead align="right">Tokens In</TableHead>
                <TableHead align="right">Tokens Out</TableHead>
                <TableHead align="right">Latency</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {SAMPLE_REQUESTS.map((req) => (
                <TableRow key={req.id}>
                  <TableCell>
                    <span className="gw-table-dimmed">{req.time}</span>
                  </TableCell>
                  <TableCell>
                    <code className="gw-code-id">{req.id}</code>
                  </TableCell>
                  <TableCell>
                    <span className="gw-model-badge">{req.model}</span>
                  </TableCell>
                  <TableCell>
                    <StatusPill
                      label={`${req.statusCode}`}
                    />
                  </TableCell>
                  <TableCell align="right">
                    <span className="gw-num-tabular">{req.tokensIn.toLocaleString()}</span>
                  </TableCell>
                  <TableCell align="right">
                    <span className="gw-num-tabular">{req.tokensOut.toLocaleString()}</span>
                  </TableCell>
                  <TableCell align="right">
                    <span className="gw-num-tabular">{req.latencyMs}ms</span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
};

export default RequestsPage;
