import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router-dom"
import { AuthProvider, WebProviders } from "@agentserver/v2-web-shared"
import "@agentserver/v2-web-shared/styles.css"
import "./browser.css"
import { BrowserApp } from "./browser-app"

const root = document.getElementById("root")
if (!root) throw new Error("Browser root element is missing.")
createRoot(root).render(<StrictMode><WebProviders><AuthProvider mode="browser"><BrowserRouter><BrowserApp /></BrowserRouter></AuthProvider></WebProviders></StrictMode>)
