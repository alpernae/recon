@"
package pipeline

import (
"context"
"github.com/alpernae/recon/internal/config"
)

func (p *Pipeline) RunAsync(ctx context.Context, opts config.RunOptions, decision config.RuntimeDecision) error {
if err := p.prepareRun(ctx, opts, decision); err != nil {
return err
}
p.logf("[+] Starting RECON ASYNC streaming workflow on %s\n", opts.Domain)
return p.Run(ctx, opts, decision)
}
"@ | Out-File -Encoding UTF8 .\internal\pipeline\async.go
