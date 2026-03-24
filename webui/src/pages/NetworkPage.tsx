import { useZones, useVNets, useFirewallRules } from '../hooks/useApi'

export default function NetworkPage() {
  const { data: zones, isLoading: zonesLoading } = useZones()
  const { data: vnets, isLoading: vnetsLoading } = useVNets()
  const { data: rules, isLoading: rulesLoading } = useFirewallRules()

  return (
    <div>
      <h2 className="mb-6 text-xl font-semibold text-slate-800">Network</h2>

      {/* Zones */}
      <h3 className="mb-3 text-sm font-semibold uppercase text-slate-500">SDN Zones</h3>
      {zonesLoading ? (
        <p className="text-slate-500">Loading...</p>
      ) : (
        <div className="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {(zones ?? []).map((zone) => (
            <div key={zone.name} className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
              <div className="flex items-center justify-between">
                <p className="font-medium text-slate-900">{zone.name}</p>
                <span className="rounded bg-indigo-100 px-2 py-0.5 text-xs font-medium text-indigo-700">
                  {zone.type}
                </span>
              </div>
              <div className="mt-2 text-xs text-slate-500">
                <p>Bridge: {zone.bridge}</p>
                <p>MTU: {zone.mtu}</p>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* VNets */}
      <h3 className="mb-3 text-sm font-semibold uppercase text-slate-500">Virtual Networks</h3>
      {vnetsLoading ? (
        <p className="text-slate-500">Loading...</p>
      ) : (
        <div className="mb-8 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-slate-200">
            <thead className="bg-slate-50">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Name</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Zone</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Tag</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Subnet</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Gateway</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {(vnets ?? []).length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-slate-400">
                    No virtual networks.
                  </td>
                </tr>
              ) : (
                (vnets ?? []).map((vnet) => (
                  <tr key={vnet.name} className="hover:bg-slate-50">
                    <td className="px-4 py-3 text-sm font-medium text-slate-900">{vnet.name}</td>
                    <td className="px-4 py-3 text-sm text-slate-500">{vnet.zone}</td>
                    <td className="px-4 py-3 text-sm text-slate-600">{vnet.tag}</td>
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{vnet.subnet}</td>
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{vnet.gateway}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* Firewall */}
      <h3 className="mb-3 text-sm font-semibold uppercase text-slate-500">Firewall Rules</h3>
      {rulesLoading ? (
        <p className="text-slate-500">Loading...</p>
      ) : (
        <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-slate-200">
            <thead className="bg-slate-50">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Direction</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Action</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Proto</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">DPort</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Source</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Comment</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {(rules ?? []).length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-6 text-center text-sm text-slate-400">
                    No firewall rules.
                  </td>
                </tr>
              ) : (
                (rules ?? []).map((rule) => (
                  <tr key={rule.id} className="hover:bg-slate-50">
                    <td className="px-4 py-3 text-sm text-slate-600">{rule.direction}</td>
                    <td className="px-4 py-3 text-sm">
                      <span
                        className={`rounded px-1.5 py-0.5 text-xs font-medium ${
                          rule.action === 'accept'
                            ? 'bg-green-100 text-green-700'
                            : 'bg-red-100 text-red-700'
                        }`}
                      >
                        {rule.action}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-sm text-slate-600">{rule.proto}</td>
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{rule.dport}</td>
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{rule.source}</td>
                    <td className="px-4 py-3 text-sm text-slate-400">{rule.comment}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
