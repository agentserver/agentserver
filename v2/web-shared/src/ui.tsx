import * as DialogPrimitive from "@radix-ui/react-dialog"
import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu"
import * as TooltipPrimitive from "@radix-ui/react-tooltip"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import { Check, ChevronRight, X } from "lucide-react"
import { forwardRef, type ButtonHTMLAttributes, type HTMLAttributes, type InputHTMLAttributes, type LabelHTMLAttributes, type ReactNode } from "react"
import { cn } from "./utils"

const buttonVariants = cva("ui-button", {
  variants: {
    variant: { default: "ui-button-default", secondary: "ui-button-secondary", ghost: "ui-button-ghost", outline: "ui-button-outline", destructive: "ui-button-destructive" },
    size: { default: "ui-button-md", sm: "ui-button-sm", icon: "ui-button-icon", lg: "ui-button-lg" },
  },
  defaultVariants: { variant: "default", size: "default" },
})

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, asChild, ...props }, ref) => {
  const Component = asChild ? Slot : "button"
  return <Component ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
})
Button.displayName = "Button"

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(({ className, ...props }, ref) => (
  <input ref={ref} className={cn("ui-input", className)} {...props} />
))
Input.displayName = "Input"

export const Label = forwardRef<HTMLLabelElement, LabelHTMLAttributes<HTMLLabelElement>>(({ className, ...props }, ref) => (
  <label ref={ref} className={cn("ui-label", className)} {...props} />
))
Label.displayName = "Label"

export const Textarea = forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(({ className, ...props }, ref) => (
  <textarea ref={ref} className={cn("ui-textarea", className)} {...props} />
))
Textarea.displayName = "Textarea"

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("ui-card", className)} {...props} />
}

export function Badge({ className, tone = "neutral", ...props }: HTMLAttributes<HTMLSpanElement> & { tone?: "neutral" | "success" | "warning" | "danger" | "info" }) {
  return <span className={cn("ui-badge", `ui-badge-${tone}`, className)} {...props} />
}

export function EmptyState({ icon, title, description, action }: { icon?: ReactNode; title: string; description?: string; action?: ReactNode }) {
  return <div className="empty-state">
    {icon ? <div className="empty-state-icon">{icon}</div> : null}
    <h3>{title}</h3>
    {description ? <p>{description}</p> : null}
    {action ? <div className="empty-state-action">{action}</div> : null}
  </div>
}

export function PageHeader({ eyebrow, title, description, actions }: { eyebrow?: string; title: string; description?: string; actions?: ReactNode }) {
  return <header className="page-header">
    <div>{eyebrow ? <div className="page-eyebrow">{eyebrow}</div> : null}<h1>{title}</h1>{description ? <p>{description}</p> : null}</div>
    {actions ? <div className="page-actions">{actions}</div> : null}
  </header>
}

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

export function DialogContent({ title, description, children, className }: { title: string; description?: string; children: ReactNode; className?: string }) {
  return <DialogPrimitive.Portal>
    <DialogPrimitive.Overlay className="dialog-overlay" />
    <DialogPrimitive.Content className={cn("dialog-content", className)}>
      <div className="dialog-heading"><DialogPrimitive.Title>{title}</DialogPrimitive.Title>{description ? <DialogPrimitive.Description>{description}</DialogPrimitive.Description> : null}</div>
      {children}
      <DialogPrimitive.Close className="dialog-close" aria-label="Close"><X size={16} /></DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
}

export const DropdownMenu = DropdownMenuPrimitive.Root
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger

export function DropdownMenuContent({ children, align = "start", className }: { children: ReactNode; align?: "start" | "center" | "end"; className?: string }) {
  return <DropdownMenuPrimitive.Portal><DropdownMenuPrimitive.Content align={align} sideOffset={6} className={cn("dropdown-content", className)}>{children}</DropdownMenuPrimitive.Content></DropdownMenuPrimitive.Portal>
}

export function DropdownMenuItem({ children, onSelect, inset, className }: { children: ReactNode; onSelect?: () => void; inset?: boolean; className?: string }) {
  return <DropdownMenuPrimitive.Item className={cn("dropdown-item", inset && "dropdown-item-inset", className)} {...(onSelect ? { onSelect } : {})}>{children}</DropdownMenuPrimitive.Item>
}

export function DropdownMenuLabel({ children }: { children: ReactNode }) { return <DropdownMenuPrimitive.Label className="dropdown-label">{children}</DropdownMenuPrimitive.Label> }
export function DropdownMenuSeparator() { return <DropdownMenuPrimitive.Separator className="dropdown-separator" /> }

export function Tooltip({ label, children }: { label: string; children: ReactNode }) {
  return <TooltipPrimitive.Provider delayDuration={350}><TooltipPrimitive.Root><TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger><TooltipPrimitive.Portal><TooltipPrimitive.Content side="right" sideOffset={8} className="tooltip-content">{label}<TooltipPrimitive.Arrow className="tooltip-arrow" /></TooltipPrimitive.Content></TooltipPrimitive.Portal></TooltipPrimitive.Root></TooltipPrimitive.Provider>
}

export function NativeSelect({ className, children, ...props }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={cn("ui-select", className)} {...props}>{children}</select>
}

export function StatusDot({ active = false }: { active?: boolean }) { return <span className={cn("status-dot", active && "status-dot-active")} /> }

export function MenuCheck({ checked }: { checked: boolean }) { return checked ? <Check size={14} /> : <span className="menu-check-placeholder" /> }
export function MenuChevron() { return <ChevronRight size={14} /> }
