---
title: ModelServing In-Place Rolling Update
authors:
  - "@kube-gopher"
reviewers:
  - TBD
approvers:
  - TBD

creation-date: 2026-08-14
---

## ModelServing In-Place Rolling Update

### Summary

This proposal introduces an explicit `InPlaceRollingUpdate` strategy for `ModelServing`. When an operator changes a regular container image, Kthena patches `spec.containers[*].image` on the existing entry and worker Pods. Kubelet then restarts only the affected containers on their assigned nodes. A successful rollout does not delete or reschedule the Pods, so their names, UIDs, IPs, node assignments, network identities, and Pod-scoped volumes are preserved.

The strategy is intentionally limited to image-only Pod template changes. The validating webhook rejects unsupported changes instead of silently falling back to replacement, and the controller repeats the same safety check before it mutates a Pod. Rollout selection and concurrency remain ServingGroup-scoped: `partition` selects the groups that may change, and `maxUnavailable` limits how many groups may be unavailable or in flight.

A durable per-ServingGroup revision record reserves the rollout slot before any Pod is patched, and a per-Pod update marker records the target revision and the container state observed before the patch. From that state, the controller can distinguish the kubelet restart expected for the new image from an unrelated container failure, resume after a controller restart, and hold the availability budget as soon as an update begins. A group completes only after the target container incarnations are running the intended images and every required Pod is Ready. An unusable image stalls the selected group without causing replacement churn.

The existing `ServingGroupRollingUpdate` remains the default, and `RoleRollingUpdate` remains available for arbitrary role template changes. Neither existing strategy changes behavior.

### Motivation

`ServingGroupRollingUpdate` and `RoleRollingUpdate` currently replace outdated workloads. The ServingGroup strategy deletes all Pods, Services, and the PodGroup belonging to a selected ServingGroup. The Role strategy deletes the Pods and Services belonging to selected Role replicas. Replacement is necessary when immutable Pod fields change, but it is unnecessarily expensive when the only desired change is a container image.

For an image-only update, replacement loses useful state that Kubernetes can preserve:

- the Pod UID, name, and IP;
- the assigned node and allocated device placement;
- Pod-scoped volumes such as `emptyDir`, including any downloaded model artifacts or caches stored there;
- existing Services and PodGroups; and
- scheduling and gang-admission decisions already made for the workload.

Large inference workloads are particularly sensitive to rescheduling, image distribution, topology, accelerator placement, model initialization, and cache warm-up. An in-place rolling update does not preserve process memory or avoid restarting the affected inference process, but it avoids repeating unrelated control-plane, scheduling, and data-preparation work.

Directly patching Pod images is not sufficient by itself. The ModelServing controller currently treats a container restart as a failure and may delete the Pod immediately under the default recovery settings. Its rollout status also assumes that every Pod in a ServingGroup has one homogeneous revision. ModelServing therefore needs an explicit strategy that coordinates image mutation with availability budgets, recovery, revision persistence, controller restarts, and status reporting.

#### Goals

- Add an opt-in rollout strategy for regular container image-only changes.
- Preserve Pod identity, node assignment, Services, and PodGroups during a successful image rollout.
- Reject or block any rollout that cannot be proven safe for in-place mutation.
- Preserve the existing ServingGroup-level `maxUnavailable` and `partition` behavior.
- Ensure that a patched-but-not-yet-restarted Pod immediately consumes rollout budget.
- Persist the completed and reserved revision of every ServingGroup independently.
- Distinguish an expected image restart from a subsequent container failure.
- Stop without replacement churn when a target image cannot start, and permit recovery by changing the desired image.
- Reconstruct rollout state correctly after controller restart or informer resynchronization.
- Define revision, replica-count, availability, condition, event, and `observedGeneration` semantics.
- Keep existing APIs and rollout behavior backward compatible.

#### Non-Goals

- Supporting arbitrary Pod template changes in place.
- Changing the default rollout strategy.
- Providing a zero-downtime guarantee for an individual container or ServingGroup.
- Preserving process memory or a container's writable layer across the restart.
- Updating init-container or ephemeral-container images in place.
- Forcing a restart or image pull when the desired image reference string is unchanged.
- Adding a Role-scoped in-place rollout in the initial version.
- Automatically rolling back to a previous image after a timeout.
- Replacing Kubernetes image-pull, readiness, startup-probe, or container-restart behavior.

### Proposal

Users select the strategy through `spec.rolloutStrategy.type`. The existing top-level `RollingUpdateConfiguration` is reused, and its values continue to be measured in ServingGroups.

```yaml
apiVersion: workload.serving.volcano.sh/v1alpha1
kind: ModelServing
metadata:
  name: llama
spec:
  replicas: 4
  rolloutStrategy:
    type: InPlaceRollingUpdate
    rollingUpdateConfiguration:
      maxUnavailable: 1
      partition: 0
  template:
    roles:
      - name: server
        replicas: 1
        workerReplicas: 0
        entryTemplate:
          spec:
            containers:
              - name: inference
                image: example.com/inference:v2
```

When the desired image changes, the controller calculates the target revision, identifies outdated ServingGroups outside `partition`, and selects as many groups as the availability budget permits. For each selected group, it patches all owned entry and worker Pods to the target revision metadata and patches only the regular container image fields that differ. Kubelet processes those image changes without creating replacement Pods.

A normal image patch produces Pod update notifications rather than Pod delete and add notifications. The controller observes those updates until every changed container has a new, verified target-image incarnation and every required Pod in the ServingGroup is Ready. It then completes the durable markers and proceeds with the next eligible group. If only some Roles or containers changed, unaffected Pods receive revision metadata without restarting so the complete ServingGroup still converges to one revision.

The validating webhook compares the old and new `ModelServing` objects. Changes to replicas and rollout controls remain valid, but Role template differences must be limited to regular container images while `InPlaceRollingUpdate` is effective. Unsupported changes receive a field-specific validation error. The controller independently repeats this comparison against the persisted historical revision because admission may be disabled or bypassed.

The strategy changes only rollout-caused replacement. Scaling, explicit deletion, and failure recovery can still create or delete resources according to existing semantics.

An update is eligible when the desired Role templates and the historical template of every selected workload differ only in `EntryTemplate.Spec.Containers[*].Image` or `WorkerTemplate.Spec.Containers[*].Image`. Containers are matched by their stable position and name. Container count, name, ordering, and every other revision-affecting field must remain equal.

The controller treats one ServingGroup as the rollout unit. Once selected, it first persists the target as that group's explicit rollout reservation. It then marks every owned Pod in the group as part of the same in-flight update and patches it to the target ModelServing revision and desired role-template hash. Pods whose regular container images differ also receive the eligible image changes. Pods whose images already match receive a metadata-only patch and do not restart. This is required because the ModelServing revision represents the complete set of Role templates, including Roles and containers that did not change in a particular Pod. The group consumes one unavailable slot from the successful reservation write, before the first Pod patch and even if kubelet has not yet changed Pod readiness. No additional group is reserved unless the global availability budget permits it.

If an image does not pull or the new container never becomes Ready, the group remains in flight and consumes its budget. The controller does not delete it merely because the expected restart failed. A user can change the desired image to a working image, and the controller prioritizes the already-unavailable group and patches it directly to the latest desired revision.

#### User Stories

##### Story 1: Upgrade a large inference workload without rescheduling

An operator runs four ready ServingGroups whose Pods have expensive accelerator allocations and topology constraints, with `maxUnavailable: 1`. The operator changes only the inference container image. ModelServing updates one ServingGroup at a time. Kubelet restarts the affected containers, while each Pod retains its UID, IP, and node assignment. The workload keeps its existing placement and avoids another scheduling cycle. After the group is running the target containers and all required Pods are Ready, the next ServingGroup is updated.

##### Story 2: Recover from an invalid image

An operator changes the image to an invalid reference. One ServingGroup is patched and becomes unavailable. The remaining three ServingGroups stay on the previous image, and no additional group is patched because the budget is exhausted. ModelServing reports a stalled in-place rollout. The operator changes the desired image to a valid reference, and the unavailable ServingGroup is patched first and recovers without Pod replacement.

##### Story 3: Reject an unsafe template change

An operator selects `InPlaceRollingUpdate` and changes an environment variable together with the image. The validating webhook rejects the update because the environment variable is immutable on an existing Pod. If admission validation is bypassed, the controller detects the same difference from the stored historical revision, records an `InPlaceUpdateBlocked` reason, and performs no Pod mutation or deletion. The operator can switch to `ServingGroupRollingUpdate` or `RoleRollingUpdate` to apply the arbitrary template change.

##### Story 4: Resume after a controller restart

The controller restarts after patching some Pods in a ServingGroup but before kubelet finishes restarting their containers. The replacement controller reads the group's persisted reservation and the target revision and pre-update container observations from Pod annotations, reconstructs the group as in flight, and waits for it to become Ready. It does not start another group or misclassify the expected restart as a failure.

#### Notes/Constraints/Caveats

- An in-place rolling update restarts affected containers. Requests served by those containers can still be interrupted.
- The strategy does not add a custom Pod readiness gate or guarantee that traffic drains before kubelet restarts a container. Existing readiness probes and consumers determine when traffic stops reaching the Pod.
- Kubernetes also permits updates to init-container images, but this proposal excludes them because rerunning completed init containers has different lifecycle and dependency semantics.
- A new image reference must differ from the old string. Reusing an unchanged mutable tag does not change PodSpec and does not force kubelet to pull or restart.
- `imagePullPolicy` is not changed. Pull behavior follows the policy already present on the Pod.
- Unchanged containers in a multi-container Pod are expected to retain their container IDs, but applications must tolerate the changed container restarting independently.
- `OnPodCreate` plugin hooks are not executed for an existing Pod. Plugins must explicitly declare `InPlaceRollingUpdate` compatibility; an unknown or incompatible plugin blocks this strategy. `OnPodReady` continues to run after readiness is observed.
- A Role replica-count change remains a scaling operation. In an existing stable group, new Role replicas use that group's persisted completed revision, including its historical templates and plugin configuration, rather than the latest desired revision. In a group with an active reservation, they use the reserved revision. No unreserved group can acquire a rollout reservation until requested Role scaling has converged, so transient unavailability of scale-created Pods is not an "already unavailable" exemption. Existing unavailable or reserved groups still count against the budget before additional in-place mutations begin.
- A `workerReplicas` change is not scaling-only: it changes Pod topology and generated environment. It is therefore ineligible for in-place update.
- `ServingGroupRecreate`, `RoleRecreate`, and `None` remain valid recovery policies. The rollout controller suppresses recovery only for a verified controller-initiated image restart; otherwise the selected recovery policy retains precedence.

### Design Details

#### API

The API adds one enum value:

```go
const (
    ServingGroupRollingUpdate RolloutStrategyType = "ServingGroupRollingUpdate"
    RoleRollingUpdate         RolloutStrategyType = "RoleRollingUpdate"
    InPlaceRollingUpdate      RolloutStrategyType = "InPlaceRollingUpdate"
)
```

`RollingUpdateConfiguration` is valid for both `ServingGroupRollingUpdate` and `InPlaceRollingUpdate`:

- `maxUnavailable` is the maximum number of ServingGroups that may be unavailable or in flight. It retains the current default of `1`, percentage rounding, and non-zero validation.
- `partition` prevents new rollout reservations for the first N ServingGroups in ordinal-sorted order. Protected groups remain on their individually recorded revision and are not patched unless they already had a reservation when `partition` was raised; an already-reserved group finishes that target instead of freezing in a mixed state.

An additive status field persists the completed revision and any active rollout reservation independently for every existing ordinal:

```go
type ServingGroupRevisionStatus struct {
    Ordinal         int32  `json:"ordinal"`
    CurrentRevision string `json:"currentRevision"`
    UpdateRevision  string `json:"updateRevision,omitempty"`
}

type ModelServingStatus struct {
    // Existing fields omitted.
    // +optional
    // +listType=map
    // +listMapKey=ordinal
    ServingGroupRevisions []ServingGroupRevisionStatus `json:"servingGroupRevisions,omitempty"`
}
```

`currentRevision` is the last revision to which the whole group converged. A non-empty per-group `updateRevision` is the durable, explicit reservation for an in-flight group. The list is controller-owned status, keyed by ordinal, and is not derived solely from the in-memory datastore.

The CRD enum, generated clients, deepcopy artifacts where applicable, Helm-embedded CRD, generated CRD reference, examples, and user documentation are regenerated through `make generate`.

The default remains `ServingGroupRollingUpdate`, so existing objects and manifests are unaffected.

No new user-facing Pod-level API is introduced. The rollout annotation and revision labels are controller-owned state, not configuration inputs.

#### Admission and Eligibility

For an UPDATE admission request whose effective new strategy is `InPlaceRollingUpdate`, the webhook decodes both the old and new ModelServing objects and verifies all of the following:

1. Roles have the same names and order.
2. Entry and worker templates have the same presence and structure.
3. Regular containers have the same count, names, and order.
4. After replacing every regular container image in the new template with the corresponding old image, the Role templates are semantically equal.
5. Init-container images and all non-image fields are unchanged.
6. Plugin configuration is semantically unchanged while the in-place strategy remains effective, whether or not a Role template also changed, and every configured plugin declares compatibility.

For a CREATE request, there is no historical template to compare. The webhook validates the strategy, rollout controls, and plugin compatibility, and the controller creates Pods directly from the desired template without an active update marker.

Replica counts, `maxUnavailable`, and `partition` are normalized consistently with the existing revision hash. They may change independently because they are reconciled as scaling or rollout-control operations rather than mutations to an existing Pod's immutable fields.

Admission validation improves feedback but is not the safety boundary. Before reserving or patching each candidate group, the controller loads the immutable snapshot for the group's persisted revision from its `ControllerRevision` and repeats the image-only template comparison and plugin-configuration comparison against the current desired state. This catches legacy objects, webhook bypass, partitioned groups that are several revisions behind, and target changes during an active rollout.

If the historical revision is missing, cannot be decoded, or does not contain recoverable plugin configuration, the candidate is blocked. The controller does not interpret a legacy Role-only snapshot as an empty plugin list and does not infer that a change is safe from a hash mismatch alone.

An operator can apply an arbitrary template change by making an existing replacement strategy effective. Switching away from `InPlaceRollingUpdate` and applying the new template in the same request is valid because the new strategy explicitly authorizes replacement.

#### Controller Architecture

The existing `ModelServingController` remains the only owner of reconciliation. The implementation separates rollout execution behind an internal strategy handler, but continues to use the controller's existing informer caches, work queue, datastore, Kubernetes clients, revision calculation, and status update path. No second controller or user-facing rollout resource is introduced.

Conceptually, rollout dispatch becomes:

```text
ServingGroupRollingUpdate -> delete and recreate outdated ServingGroups
RoleRollingUpdate         -> delete and recreate outdated Role instances
InPlaceRollingUpdate      -> patch images and revision metadata in outdated ServingGroups
```

Only the rollout action changes. Scaling remains ordered before rollout, and explicit deletion and failure recovery remain owned by their existing reconciliation paths. Scaling an existing group renders new Role replicas from that group's persisted completed revision, or from its reserved revision if it is already in flight; it never chooses the global desired revision merely because scaling runs first. If requested Role scaling has not converged, reconciliation may continue groups that already hold reservations but does not reserve any additional group. The in-place handler uses the corresponding `ControllerRevision` and role-template hashes to compare the observed and desired templates, and it performs the defensive eligibility check immediately before reserving a group and again before building each Pod patch.

#### Revision Persistence

`ModelServingRevision` and `CalRoleTemplateHash` continue hashing the complete normalized Role templates. An image change therefore produces a new update revision and role-template hash without a new hash format. Plugin configuration is persisted and compared separately; it is not assumed from a Role-template hash.

Every newly created `ControllerRevision` contains a versioned snapshot with the normalized Role templates, the normalized applied `PluginSpec` list, and its canonical plugin-configuration hash. These snapshots are immutable: if an existing revision name has different bytes or lacks the plugin portion of the schema, the controller reports a history conflict instead of overwriting it. This makes an admission-bypassed image-and-plugin change detectable from the source snapshot and preserves the plugin inputs needed when a historical Pod must be recreated.

Before creating a ServingGroup at a new revision, the controller persists the desired snapshot. For an in-place rollout, it first proves eligibility from the candidate group's source snapshot and only then persists the desired snapshot, before writing a reservation. An unsafe admission-bypassed request therefore cannot poison immutable history with an unapplied plugin configuration. Failure to validate or persist the target stops reconciliation without mutating Pods. Before creating a Role replica in an existing group, the controller loads both Role templates and plugin configuration from the snapshot named by that group's status record. A missing or incomplete snapshot blocks creation rather than falling back to the latest `ModelServing` spec.

For each observed ordinal, the controller also maintains `status.servingGroupRevisions`. It records `currentRevision` when the group is first created or reconstructed from unambiguous homogeneous Pod state. Before the first in-place Pod patch, it writes the target to that entry's `updateRevision`; a failed or conflicting status write means no Pod is patched. After every required Pod has completed, it atomically advances the entry's `currentRevision` to the target and clears its `updateRevision`. The Pod markers are not cleared until that status update succeeds.

The per-group entry is retained while the ordinal exists, including when all of its Pods are temporarily absent. Recovery and scale-created Pods use the entry rather than global `status.currentRevision`. For a pre-existing ordinal with neither a persisted entry nor unambiguous live Pod labels, the controller reports `InPlaceUpdateBlocked` and does not recreate it from a guessed revision. An entry is removed only after scale-down has conclusively removed that ordinal's resources.

ControllerRevision cleanup is extended to preserve revisions referenced by:

- `status.currentRevision`;
- `status.updateRevision`;
- every `currentRevision` and `updateRevision` in `status.servingGroupRevisions`;
- any owned Pod's revision label; and
- any active in-place update marker.

This permits more than two revisions during an interrupted rollout or rapid roll-forward without losing the information needed for eligibility checks.

#### Durable Pod State

The controller records a versioned, controller-owned annotation on every Pod in a selected ServingGroup. The exact serialization is an implementation detail, but it contains at least:

- the Pod UID and controlling ModelServing UID;
- the target ModelServing revision and role-template hash;
- update phase and start time;
- each changed container name and desired image, with an empty list for a metadata-only Pod;
- each changed container's ID and restart count observed before patching; and
- the latest acknowledged restart count after successful completion.

Each Pod is changed with one JSON Patch that begins with `test` operations for its `metadata.resourceVersion`, owner identity, and expected container names and order. The patch writes the target ModelServing revision label, desired role-template-hash label, and active state, and replaces only the regular container image fields that differ. A metadata-only Pod patch contains no PodSpec replacement. No regenerated full PodSpec is submitted. A failed precondition or API conflict causes the controller to fetch the live Pod, repeat all ownership and eligibility checks, and retry from the new resource version.

When a Pod completes its update, the active portion of the marker is changed to a compact completed record containing the target revision and acknowledged restart counts. This record is required because Kubernetes restart counts are cumulative. Without an acknowledged baseline, the existing recovery logic would interpret an expected historical image restart as a new failure after controller restart or a later readiness transition.

The controller ignores malformed, stale, or ownership-mismatched state and reports it. It never suppresses recovery based only on an annotation name.

#### Rollout Reconciliation

The controller derives a ServingGroup's rollout state from its durable per-group status and Pods rather than treating the in-memory datastore as authoritative. The normal state progression is:

```text
Current and Ready -> Reserved -> In Flight -> Target and Ready
                                  |
                                  +-> Stalled -> In Flight when the target becomes viable or is superseded
```

`Reserved` begins when the per-group `updateRevision` status write succeeds. `In Flight` begins with the first accepted Pod patch, including a metadata-only patch. `Stalled` retains the active marker and availability reservation; it is not a transition to replacement. An unsafe candidate is `Blocked` before any Pod is mutated. These are derived reconciliation states and condition reasons, not new user-facing API fields.

In-place reconciliation proceeds as follows:

1. Calculate the desired revision without mutating history or Pods.
2. List owned Pods and combine them with `status.servingGroupRevisions` to reconstruct Role replicas, observed revisions, reservations, active markers, and readiness. A partially targeting group without a matching persisted reservation is inconsistent state: it blocks further in-place mutation and cannot acquire a free slot merely because it is partial.
3. Validate and reconcile groups with existing reservations first. If a reserved group remains outside `partition`, its reservation may be superseded directly by the latest eligible image target only after the source comparison succeeds and the latest immutable snapshot is persisted. If it has become protected, it continues only to its already-reserved target.
4. If a requested Role scale operation has not converged, report `Progressing`, continue only the reservations from step 3, and stop before selecting unreserved groups. A Pod that is unready because it was just created for scaling therefore cannot make its group eligible for slot-free rollout work.
5. Apply `partition` to the remaining unreserved groups, so protected groups cannot acquire a new reservation.
6. For each remaining candidate, load its immutable source snapshot and compare both templates and plugin configuration with the desired state. Block an unsafe candidate; after eligibility succeeds, persist the desired snapshot before selection.
7. Classify eligible non-target groups into actually unavailable and healthy groups. Actually unavailable groups are selected first, followed by the existing descending-ordinal ordering.
8. Calculate budget using all groups, including protected groups: a group counts as unavailable when it is not fully Ready or has a persisted `updateRevision` reservation.
9. Persist a reservation for selected groups before patching them. An actually unavailable group can be reserved without consuming another slot; a healthy group can be reserved only up to the remaining budget. If any reservation write fails, that group receives no Pod patches.
10. For every reserved group, patch every owned Pod to the reserved revision metadata and active marker; include image replacements only for Pods with differing eligible images. A partial group remains reserved until every required Pod completes.
11. Recompute group revision, readiness, replica counts, conditions, and events from observed Pod state. Advance and clear a group's reservation only after group-wide completion.

Partition changes use finish-in-flight semantics. Raising `partition` never creates a new reservation for a newly protected group, but it also does not cancel a reservation or freeze a group after some Pods have been patched. That group finishes the exact revision already recorded in its reservation and then remains protected there. While protected, it is not retargeted to a later desired revision. Lowering `partition` makes it eligible for a new reservation. This exception is limited to a reservation persisted before the partition change; a Pod label or partial target without that record is not sufficient.

A Pod is observed complete for its group reservation only when:

- the Pod spec contains every regular container image from the reserved snapshot, and its ModelServing revision and role-template-hash labels contain the reserved target values;
- for every container recorded as changed, its current container ID is non-empty and differs from the ID recorded before the patch;
- for every container recorded as changed, its `status.containerStatuses` entry identifies the desired image according to the comparison rules below;
- every changed container is Ready; and
- the Pod is Ready.

Image identity comparison uses Kubernetes-compatible Docker/OCI reference normalization. Familiar names are expanded to their default registry and namespace, and a missing tag is normalized to `latest`. For a reserved name-and-tag reference, the normalized target reference must equal the normalized `ContainerStatus.Image`. For a reserved digest reference, the digest must equal the digest in `ContainerStatus.ImageID` after removing any runtime transport prefix; an equivalent digest in `ContainerStatus.Image` is also accepted. A name-and-tag reference is not inferred to equal an arbitrary digest from `ImageID`. If the runtime does not expose a parseable, comparable identity, the container remains incomplete and the controller reports `UpdateInProgress=True` with reason `ImageIdentityUnverifiable` rather than falsely completing the update.

The reservation completes only after every owned Pod has the reserved PodSpec, ModelServing revision label, and role-template-hash label, including Pods that required only a metadata update. The group becomes globally updated only when that reserved revision also equals global `status.updateRevision`. It becomes available only after every required Pod satisfies the completion checks above. These are separate transitions: a group whose specs target its reservation may remain unavailable while an image is pulling or a startup probe is running.

Scaling reconciliation remains ordered before rollout reconciliation. A newly created ServingGroup uses the revision selected by existing partition rules and receives its per-group status entry before its resources are created. A new Role replica in an existing group uses the group's persisted `currentRevision`, or its `updateRevision` if already reserved. Consequently, an image change submitted with a Role `replicas` scale-up creates old-revision replicas in every stable old group, waits for requested scaling to converge, and only then permits unreserved groups to enter rollout selection. Only groups that obtain a rollout reservation receive target metadata or images. Scale-down and recovery deletion are not presented as in-place rollout operations and retain their existing behavior.

#### Availability Semantics

`maxUnavailable` is enforced against actual service capacity and reserved in-flight work:

```text
unavailable = groups that are not fully Ready
            union groups with a persisted updateRevision reservation

remainingBudget = max(0, maxUnavailable - cardinality(unavailable))
```

The union prevents double counting a group that is both reserved and not Ready. Because the reservation is persisted before Pod mutation and retained until group-wide completion, a group whose old containers still appear Ready remains unavailable for budget purposes across controller crashes and metadata-only Pod completion. Unavailable protected groups also consume budget because they reduce real serving capacity. Partial target state alone is deliberately not a reservation and never grants permission to patch the rest of a group; a partial group without the matching status record fails closed.

The strategy does not create surge capacity. If existing unrelated failures exhaust `maxUnavailable`, no new healthy group is patched. An outdated group that is already unavailable can still be selected because doing so does not increase total unavailability, consistent with the existing replacement rollout preference for unhealthy outdated groups.

#### Traffic and Readiness Semantics

The active update marker is a rollout and budget signal; it does not directly change Pod readiness or remove an endpoint from traffic. The controller does not add a custom Pod readiness gate and does not patch `pods/status`. A readiness gate would not provide a uniform upgrade path for existing Pods without recreation, and Kthena's Role headless Services currently set `publishNotReadyAddresses: true`, so changing Pod readiness would not by itself guarantee removal from those discovery endpoints.

After kubelet begins the restart, normal container readiness and startup probes determine when the Pod becomes not Ready and when it returns to Ready. Routers, clients, and peer processes must tolerate the resulting connection interruption. The availability controller remains conservative by reserving budget before that readiness transition is observed, but `InPlaceRollingUpdate` is not a request-draining protocol. An explicit application-aware drain handshake can be proposed separately if workloads need a pre-restart quiescence guarantee.

#### Recovery Semantics

Recovery policy and rollout ownership are separated as follows:

| Situation                                             | Behavior                                                                                                                   |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| Valid active marker and expected container transition | Mark the Role and ServingGroup unavailable, suppress restart-triggered deletion, and wait.                                 |
| Target image pull or startup failure                  | Keep the Pod and marker, hold budget, emit a warning event, and report a stalled update.                                   |
| Desired image changes during an active update         | If the group is still unprotected, revalidate eligibility, move its reservation, and patch it directly to the latest target. A newly protected group finishes its existing reservation. |
| Target containers become Ready                        | Acknowledge their restart counts, complete the marker, and resume normal recovery handling.                                |
| Restart count later exceeds the acknowledged value    | Treat it as a new failure and apply the configured `RecoveryPolicy`.                                                       |
| Pod is externally deleted during the update           | Apply the configured recovery policy; recreate from the group's persisted reserved revision, or from its completed revision when it has no reservation.                    |
| Strategy changes to a replacement strategy            | The active in-place marker no longer suppresses the explicitly selected replacement rollout.                               |
| Controller restarts                                   | Load per-group revisions and reservations, then combine them with Pod markers before selecting new work.                                           |

At completion, the controller records the observed restart count as the new acknowledged baseline only for containers included in the verified image update. An unchanged container receives no restart allowance: any increase in its restart count follows normal failure recovery. For a changed container, any increase after the completed baseline is also a new failure. A restart count lower than the stored baseline, a changed Pod UID, or a target mismatch makes the completed record stale and prevents it from suppressing recovery.

No timeout causes automatic Pod replacement in the initial implementation. While a Pod is still pulling or starting, `UpdateInProgress` remains `True` with reason `InPlaceRollingUpdate`; a known blocking state changes the reason to `InPlaceUpdateStalled`. Recovery requires a desired-image change, an explicit strategy change, or external intervention.

#### Pod Event Handling

A successful image patch updates the existing Pod and therefore enters the Pod update handler; it does not normally produce Pod delete or add events. The update handler evaluates controller-owned in-place state before applying generic restart recovery:

- A valid active marker causes the owning `ModelServing` to be re-enqueued and the Pod to be evaluated for target progress. Only the restart represented by that marker is excluded from generic failure handling.
- A completed marker supplies the acknowledged restart baseline. Later restarts and restarts of unchanged containers follow the configured `RecoveryPolicy`.
- A malformed, ownership-mismatched, or target-inconsistent marker never suppresses recovery by itself; reconciliation reports it and resolves behavior from the verified Pod and ModelServing state.

Pod deletion and addition remain exceptional lifecycle events rather than normal in-place rollout steps. If an updating Pod is deleted or evicted, the delete handler follows the configured `RecoveryPolicy`, and any recreated resources use that ordinal's persisted reserved revision, or its completed revision when no reservation exists. The controller loads the corresponding historical Role templates and plugin configuration; it never substitutes the prevalent global `currentRevision`. If the per-group record or snapshot cannot be recovered, recreation fails closed and reports `InPlaceUpdateBlocked`. An added or recreated Pod is classified from its owner, revision labels, persisted group record, desired target, and container observations before new rollout work is selected. Neither datastore state nor a non-zero `restartCount` alone is sufficient to classify a Pod as actively updating.

#### Status, Events, and Diagnostics

Existing status fields retain ServingGroup units, and the additive `servingGroupRevisions` field supplies durable per-group state:

| Field                | In-place rolling update meaning                                                                                                                                                                                                          |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `replicas`           | Number of observed ServingGroups, including in-flight groups.                                                                                                                                                                            |
| `updatedReplicas`    | Groups for which every Pod spec has the desired images, every ModelServing revision label equals `updateRevision`, and every role-template-hash label has the desired Role hash, whether Ready or not.                                   |
| `currentReplicas`    | Groups not yet fully targeting `updateRevision`, including partially patched groups.                                                                                                                                                     |
| `availableReplicas`  | Groups whose required Pods are Ready at their currently applied revision. For an in-flight group, the new container incarnations must first pass the completion checks. Ready groups still waiting on the old revision remain available. |
| `updateRevision`     | Revision calculated from the latest desired Role templates.                                                                                                                                                                              |
| `currentRevision`    | Aggregate compatibility and display value: the existing valid non-target revision, or the most prevalent non-target revision when several exist. It is never used to choose a protected group's recreation template.                    |
| `servingGroupRevisions` | One entry per existing ordinal containing its last fully applied `currentRevision` and optional reserved `updateRevision`. This is the authoritative source for per-group creation, recovery, partition protection, and revision retention. |
| `observedGeneration` | Latest generation whose safety and rollout state have been evaluated, including a blocked generation.                                                                                                                                    |

For an active unpartitioned rollout, `updatedReplicas + currentReplicas == replicas`. A partially patched ServingGroup remains current until all of its Pod specs and revision metadata have the desired values. With partition, protected groups remain current and completion is reached when all eligible groups are updated.

Condition semantics are:

- `UpdateInProgress=True`, reason `InPlaceRollingUpdate`, while any eligible group is non-target or any group, including a newly protected group, still has a reservation.
- `UpdateInProgress=True`, reason `InPlaceUpdateStalled`, when an active container reports a known blocking state such as `ErrImagePull`, `ImagePullBackOff`, or `CreateContainerError`.
- `UpdateInProgress=True`, reason `ImageIdentityUnverifiable`, when a restarted container is Ready but its runtime status does not expose an image reference that can be proven equivalent to the desired reference.
- `UpdateInProgress=True`, reason `InPlaceUpdateBlocked`, when the controller cannot prove image-only eligibility or durable revision ownership. The message identifies the first incompatible field, missing or incomplete historical snapshot, missing per-group revision, or unreserved partial group.
- `UpdateInProgress=False`, reason `RolloutComplete`, when every non-protected group has completed and no group has an outstanding reservation.
- `Progressing` continues to describe creation and scaling rather than image rollout.
- `Available` retains the existing phase-style exclusivity with `Progressing` and `UpdateInProgress`; `availableReplicas` remains the capacity signal during a rollout.

Condition updates compare and persist status, reason, and message, not only condition type and boolean value. This allows a progressing rollout to transition to stalled or blocked without changing condition type.

Normal events are emitted when a group starts and completes an in-place update. Warning events are emitted for unsafe changes, Pod patch failures, image-pull/startup stalls, malformed state, and missing ControllerRevisions. Repeated events are rate-limited by normal Kubernetes event aggregation.

Structured logs include the ModelServing namespace and name, ServingGroup, Role name and ID, Pod, target revision, and changed container names. Debug cache output exposes the derived update phase and observed revision/hash so operators can distinguish current, in-flight, stalled, and blocked work. Logs and debug output do not include registry credentials, image-pull Secret data, or the unvalidated raw annotation payload.

#### Plugin Semantics

Plugin compatibility is registry metadata rather than a required method on the existing `Plugin` interface. This avoids breaking existing plugin implementations and lets admission check capability without executing lifecycle hooks. The registry adds a capability value equivalent to:

```go
type Capabilities struct {
    InPlaceRollingUpdate bool
}
```

The existing `Register(name, factory)` path remains available and records the zero-value capabilities, so previously registered and unknown plugins are incompatible by default. A new `RegisterWithCapabilities(name, factory, capabilities)` path is used by plugins that explicitly opt in. Both the validating webhook and controller query the same registry metadata. An empty plugin list is compatible. A missing registry entry, a non-built-in plugin type, or any configured plugin whose `InPlaceRollingUpdate` capability is false blocks the strategy.

A compatible plugin must not require `OnPodCreate` to transform the target image or another immutable field as a consequence of the image change. All currently built-in plugins must be reviewed, tested, and registered with an explicit capability; compatibility is never inferred from current observed behavior.

`OnPodCreate` is not called during the patch. Existing plugin mutations remain on the Pod. `OnPodReady` is invoked through the normal ready event after the new container becomes Ready. Changing plugin configuration while `InPlaceRollingUpdate` remains effective is rejected because the historical rendered behavior cannot otherwise be proven equivalent.

The controller enforces that rule without relying on admission: it compares the desired plugin specs and canonical hash with the applied values stored in the candidate group's immutable `ControllerRevision`. Historical creation, including Role scale-up and recovery, constructs the plugin chain from that same snapshot. A Role-only legacy snapshot, an unknown snapshot schema, or a missing plugin hash is not treated as evidence of an empty configuration; the operation fails closed before any Pod mutation or creation.

#### RBAC and Security

The controller currently has Pod create, delete, get, list, and watch permissions but not Pod mutation permission. The Helm ClusterRole adds the `patch` verb for Pods. It does not add `pods/status`, impersonation, or unrelated resource permissions.

Before every patch, the controller verifies that the Pod has a controlling ModelServing owner reference with the current ModelServing UID and belongs to the expected ServingGroup and Role. The patch changes only regular container images and controller-owned metadata. Kubernetes API validation remains a final guard against mutation of immutable Pod fields.

### Test Plan

Unit tests cover:

- image-only semantic comparison for entry and worker templates;
- rejection of container reorder, rename, add/remove, init-image, environment, metadata, resource, topology, and plugin changes combined with an image rollout;
- rollout dispatch to replacement or in-place handlers without changing existing strategy behavior;
- versioned ControllerRevision snapshot lookup, plugin-spec/hash comparison, immutable-history conflicts, and fail-closed handling of legacy Role-only history;
- per-ordinal completed revisions, reservation persistence before Pod mutation, reservation completion ordering, and fail-closed handling of unreserved partial groups;
- partition selection, raising partition around an active reservation, unhealthy-first ordering, and `maxUnavailable` calculation;
- simultaneous image and Role replica scale-up from each stable group's historical revision, with no new rollout reservation until scaling converges and no target-revision partial groups;
- metadata-only updates for unaffected Pods, reserved partially patched group classification, and mixed revision reconstruction;
- durable marker parsing, stale marker rejection, and target supersession;
- changed container-ID, normalized image-identity, unobservable image-identity, and acknowledged restart-count completion;
- no restart allowance for unchanged containers, post-completion restart detection, and stale baselines after Pod recreation;
- Pod update handling for active and completed markers, plus configured recovery for exceptional delete and add events;
- plugin capability registration, fail-closed default behavior, empty-plugin-list behavior, and historical plugin-chain reconstruction for Pod creation;
- recovery suppression for expected restarts and normal recovery for later restarts;
- status counters, revision advancement, conditions, reasons, and messages;
- Pod patch conflicts, ownership mismatch, and partial patch errors;
- ControllerRevision retention for every per-group completed and reserved revision, Pod-referenced revisions, and active markers; and
- validating webhook handling of old and new objects.

Kind-based controller-manager E2E tests cover:

1. A successful image update preserves Pod names, UIDs, `spec.nodeName`, Pod IPs, Services, and PodGroups.
2. A Pod watch observes update events for the normal rollout path and no planned Pod delete or add events.
3. In a multi-container Pod, the changed container receives a new container ID while an unchanged sidecar retains its ID.
4. Entry, worker, and unaffected Role Pods in a multi-Pod ServingGroup receive the target revision metadata, while only Pods with changed images restart, before another group begins.
5. `maxUnavailable` is never exceeded, including the interval before kubelet clears readiness.
6. An invalid image stalls at the configured budget without Pod replacement or node movement.
7. Changing from an invalid image to a valid image recovers the in-flight group first and completes in place.
8. Controller restart during a partial update resumes from the persisted group reservation and Pod state without starting excess groups.
9. An image update combined with a Role replica scale-up keeps unreserved groups on their recorded revisions, waits for the scale-up to converge before new rollout selection, and still respects `maxUnavailable`.
10. Partition protects the expected ordinals, raising it lets an already-reserved group finish only its reserved target, and lowering it makes newly eligible groups update in place.
11. Successive partitioned rollouts leave protected ordinals on multiple historical revisions; deleting their only Pod and restarting the controller recreates each from its own retained snapshot.
12. Admission-bypassed plugin configuration changes are blocked from in-place mutation, including when combined with an image change.
13. Unsupported template mutations are rejected by the webhook and cause no Pod deletion in the controller's defensive path.
14. External Pod deletion during a rollout follows each supported recovery policy.
15. Switching to an existing replacement strategy permits an arbitrary template update and clears or supersedes in-place state correctly.
