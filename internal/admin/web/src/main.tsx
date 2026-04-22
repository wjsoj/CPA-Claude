import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { StatusApp } from "./StatusApp";
import { Toaster } from "@/components/ui/sonner";
import { ConfirmHost } from "@/hooks/use-confirm";
import { ThemeProvider } from "@/hooks/use-theme";
import "./styles/globals.css";

const isStatus = window.location.pathname.startsWith("/status");

createRoot(document.getElementById("app")!).render(
  <StrictMode>
    <ThemeProvider>
      {isStatus ? <StatusApp /> : <App />}
      <ConfirmHost />
      <Toaster richColors closeButton position="top-right" />
    </ThemeProvider>
  </StrictMode>,
);
