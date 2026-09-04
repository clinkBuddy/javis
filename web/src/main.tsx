import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createHashRouter, RouterProvider } from "react-router-dom";

import { Shell } from "./Shell";
import { Dashboard } from "./pages/Dashboard";
import { AppDetail } from "./pages/AppDetail";
import { Jdks } from "./pages/Jdks";
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
    ],
  },
]);

const root = document.getElementById("root");
if (!root) {
  throw new Error("#root is missing from index.html");
}

createRoot(root).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
