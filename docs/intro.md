# NVIDIA Health Validation

In an AI factory, one unhealthy GPU node can stall or fail a distributed job
running across many nodes, idling every GPU in that job, and the scheduler has
no way to know the node is bad until the job fails. NVIDIA Health Validation
answers one question before you hand a node to a workload: is this node ready?

It runs on a GPU node and checks GPUs, NVLink, CPU and NUMA topology, Xid
errors, and InfiniBand link flaps against NVIDIA-authored checks. It returns a
single verdict: PASS, INFO, WARNING, CRITICAL, or UNKNOWN. A failing check also
carries the recommended next steps, such as reset the GPU, restart the node, or
open a service request, so your automation can act on it.

Run it from a Slurm prolog or a Kubernetes pre-flight Job to keep unhealthy
nodes out of rotation before a job lands on them. It reports and does not act:
you decide what happens to the node. You can tune thresholds per node class and
add your own checks.

Each run evaluates one node and keeps no state, so run it before each job
dispatch. The verdict is a snapshot of the node at that moment.

## Where to go next

- [Integrating into a GPU cluster](/health-validation/operating/integration):
  deploy it, wire it into Slurm or Kubernetes, and act on the verdict.
- [Remediation Actions](/health-validation/operating/remediation): what each
  recommended action means and how to work through a sequence.
- [Architecture](/health-validation/getting-started/architecture): how the
  catalog, sensors, and verdict fit together.
- [FAQ](/health-validation/getting-started/faq): exit codes, skipped checks,
  and common questions.
- [Verdict JSON Schema](https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/node-verdict.schema.json):
  the verdict and check-result type definitions.
