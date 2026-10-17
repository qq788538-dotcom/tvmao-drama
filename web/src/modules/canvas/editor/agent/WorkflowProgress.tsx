import { Check, Loader2, XCircle } from 'lucide-react';

import type { AgentMessage } from './agentStore';

export function WorkflowProgress({ msg }: { msg: AgentMessage }) {
  if (!msg.workflowSteps || msg.workflowSteps.length === 0) return null;
  return (
    <div className="mt-1.5 px-2 py-1.5 bg-background/60 border border-input rounded text-[11px] flex flex-col gap-0.5">
      {msg.workflowSteps.map((s, i) => (
        <div key={i} className="flex items-center gap-1.5">
          {s.status === 'running' ? (
            <Loader2 size={11} className="animate-spin text-blue-400 shrink-0" />
          ) : s.status === 'done' ? (
            <Check size={11} className="text-emerald-400 shrink-0" />
          ) : (
            <XCircle size={11} className="text-red-400 shrink-0" />
          )}
          <span className="text-foreground truncate">{s.name}</span>
        </div>
      ))}
    </div>
  );
}
