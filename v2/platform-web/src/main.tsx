import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router-dom"
import { AuthProvider, WebProviders } from "@agentserver/v2-web-shared"
import "@agentserver/v2-web-shared/styles.css"
import "./platform.css"
import { PlatformApp } from "./platform-app"

const root = document.getElementById("root")
if (!root) throw new Error("Platform root element is missing.")

createRoot(root).render(<StrictMode><WebProviders><AuthProvider mode="platform"><BrowserRouter><PlatformApp /></BrowserRouter></AuthProvider></WebProviders></StrictMode>)
