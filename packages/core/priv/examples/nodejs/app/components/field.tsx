import * as React from "react";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// Context for compound component pattern
interface FieldContextValue {
  name: string;
  error?: string;
}

const FieldContext = React.createContext<FieldContextValue | null>(null);

const useFieldContext = () => {
  const context = React.useContext(FieldContext);
  if (!context) {
    throw new Error("Field components must be used within Field.Root");
  }
  return context;
};

// Root component
export interface FieldRootProps extends React.ComponentPropsWithoutRef<"div"> {
  name: string;
  error?: string;
}

const FieldRoot = React.forwardRef<HTMLDivElement, FieldRootProps>(
  ({ name, error, className, children, ...props }, ref) => {
    return (
      <FieldContext.Provider value={{ name, error }}>
        <div ref={ref} className={cn("space-y-2", className)} {...props}>
          {children}
        </div>
      </FieldContext.Provider>
    );
  },
);
FieldRoot.displayName = "FieldRoot";

// Label component
export interface FieldLabelProps
  extends React.ComponentPropsWithoutRef<typeof Label> {
  required?: boolean;
}

const FieldLabel = React.forwardRef<
  React.ElementRef<typeof Label>,
  FieldLabelProps
>(({ required, children, ...props }, ref) => {
  const { name } = useFieldContext();
  return (
    <Label ref={ref} htmlFor={name} {...props}>
      {children}
      {required && <span className="text-destructive ml-1">*</span>}
    </Label>
  );
});
FieldLabel.displayName = "FieldLabel";

// Control component (Input)
export interface FieldControlProps
  extends React.ComponentPropsWithoutRef<typeof Input> {}

const FieldControl = React.forwardRef<
  React.ElementRef<typeof Input>,
  FieldControlProps
>((props, ref) => {
  const { name, error } = useFieldContext();
  return (
    <>
      <Input
        ref={ref}
        id={name}
        name={name}
        aria-invalid={error ? "true" : "false"}
        aria-describedby={error ? `${name}-error` : undefined}
        {...props}
      />
      {error && (
        <p id={`${name}-error`} className="text-sm text-destructive">
          {error}
        </p>
      )}
    </>
  );
});
FieldControl.displayName = "FieldControl";

// Simple single-component API (backwards compatibility)
export interface FieldProps extends React.ComponentPropsWithoutRef<"div"> {
  label?: string;
  name: string;
  type?: string;
  error?: string;
  required?: boolean;
  defaultValue?: string;
}

const FieldSimple = React.forwardRef<HTMLDivElement, FieldProps>(
  (
    {
      label,
      name,
      type = "text",
      error,
      required,
      defaultValue,
      className,
      ...props
    },
    ref,
  ) => {
    return (
      <div ref={ref} className={cn("space-y-2", className)} {...props}>
        {label && (
          <Label htmlFor={name}>
            {label}
            {required && <span className="text-destructive ml-1">*</span>}
          </Label>
        )}
        <Input
          id={name}
          name={name}
          type={type}
          defaultValue={defaultValue}
          required={required}
          aria-invalid={error ? "true" : "false"}
          aria-describedby={error ? `${name}-error` : undefined}
        />
        {error && (
          <p id={`${name}-error`} className="text-sm text-destructive">
            {error}
          </p>
        )}
      </div>
    );
  },
);
FieldSimple.displayName = "Field";

// Export compound component
// Description was USED but never defined — `register-view.tsx` renders
// `<Field.Description>` and the composite had no such member. The hint text was
// simply never going to compile.
const FieldDescription = React.forwardRef<
  HTMLParagraphElement,
  React.HTMLAttributes<HTMLParagraphElement>
>(({ className, ...props }, ref) => (
  <p
    ref={ref}
    className={cn("text-muted-foreground text-sm", className)}
    {...props}
  />
));
FieldDescription.displayName = "FieldDescription";

export const Field = Object.assign(FieldSimple, {
  Root: FieldRoot,
  Label: FieldLabel,
  Control: FieldControl,
  Description: FieldDescription,
});
