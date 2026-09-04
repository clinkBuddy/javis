import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createHashRouter, RouterProvider } from "react-router-dom";

import { Shell } from "./Shell";
import { SessionProvider } from "./session";
import { Dashboard } from "./pages/Dashboard";
import { AppDetail } from "./pages/AppDetail";
import { Jdks } from "./pages/Jdks";
import { Users } from "./pages/Users";
import { Bans } from "./pages/Bans";
import { ChangePassword } from "./pages/ChangePassword";
import "./styles.css";

// Hash routing avoids needing the Go file server to rewrite unknown paths to
// index.html, and it keeps working if the UI is ever served from a subpath.
const router = createHashRouter([
  {
    path: "/",
    element: <Shell />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: "apps/:name", element: <AppDetail /> },
      { path: "jdks", element: <Jdks /> },
      // Not route-guarded: the server rejects these calls for anyone below
      // admin, and the navigation link is already hidden. A client-side guard
      // here would only duplicate that decision in a place that cannot
      // enforce it.
      { path: "users", element: <Users /> },
      { path: "bans", element: <Bans /> },
      { path: "account", element: <ChangePassword forced={false} /> },
    ],
  },
]);

const root = document.getElementById("root");
if (!root) {
  throw new Error("#root is missing from index.html");
}

createRoot(root).render(
  <StrictMode>
    <SessionProvider>
      <RouterProvider router={router} />
    </SessionProvider>
  </StrictMode>,
);
