import React from "react";
import { Skeleton, Card, CardContent } from "../../shared/ui";

export const RouteLoadingFallback: React.FC = () => {
  return (
    <div className="gw-route-skeleton-container" data-testid="route-loading-fallback" aria-busy="true" aria-label="Loading page content">
      <div className="gw-skeleton-kpi-grid">
        {[1, 2, 3, 4, 5].map((i) => (
          <Card key={i}>
            <CardContent>
              <div style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
                <Skeleton width="40%" height={14} />
                <Skeleton width="70%" height={28} />
                <Skeleton width="50%" height={12} />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card>
        <CardContent>
          <div style={{ display: "flex", flexDirection: "column", gap: "1rem", padding: "0.5rem 0" }}>
            <Skeleton width="30%" height={20} />
            <Skeleton width="100%" height={120} />
            <Skeleton width="80%" height={16} />
          </div>
        </CardContent>
      </Card>
    </div>
  );
};
