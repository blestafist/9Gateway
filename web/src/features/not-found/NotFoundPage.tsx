import React from "react";
import { Link } from "react-router-dom";
import { EmptyState, Button } from "../../shared/ui";
import { Compass } from "lucide-react";

export const NotFoundPage: React.FC = () => {
  return (
    <div className="gw-page-content gw-not-found-page" data-testid="not-found-page">
      <EmptyState
        icon={<Compass size={48} />}
        title="Page Not Found"
        description="The requested page route could not be found under the 9Gateway console (/ui/)."
        action={
          <Link to="/overview" style={{ textDecoration: "none" }}>
            <Button variant="primary">Return to Overview</Button>
          </Link>
        }
      />
    </div>
  );
};

export default NotFoundPage;
