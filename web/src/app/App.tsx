import React from "react";
import { SmokeStatus } from "../features/smoke";

export const App: React.FC = () => {
  return (
    <main
      style={{
        fontFamily: "system-ui, sans-serif",
        padding: "2rem",
        maxWidth: "600px",
        margin: "0 auto",
      }}
    >
      <h1>9Gateway</h1>
      <p>Embedded Web UI console smoke page.</p>
      <SmokeStatus status="operational" />
    </main>
  );
};

export default App;
