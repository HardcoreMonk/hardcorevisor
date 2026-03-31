import { useState } from 'react'
import { useImages, useCreateImage, useDeleteImage } from '../hooks/useApi'

export default function ImagesPage() {
  const { data: images, isLoading } = useImages()
  const createImage = useCreateImage()
  const deleteImage = useDeleteImage()
  const [name, setName] = useState('')
  const [format, setFormat] = useState('qcow2')
  const [path, setPath] = useState('')
  const [osType, setOsType] = useState('linux')

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault()
    createImage.mutate({ name, format, path, os_type: osType })
    setName(''); setPath('')
  }

  if (isLoading) return <p className="p-6 text-slate-400">Loading images...</p>

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold text-white">Image Registry</h1>
      <form onSubmit={handleCreate} className="flex flex-wrap gap-3">
        <input value={name} onChange={e => setName(e.target.value)} placeholder="Image Name"
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white" required />
        <input value={path} onChange={e => setPath(e.target.value)} placeholder="/path/to/image.qcow2"
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white flex-1" required />
        <select value={format} onChange={e => setFormat(e.target.value)}
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white">
          <option value="qcow2">qcow2</option>
          <option value="raw">raw</option>
          <option value="iso">iso</option>
        </select>
        <select value={osType} onChange={e => setOsType(e.target.value)}
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white">
          <option value="linux">Linux</option>
          <option value="windows">Windows</option>
        </select>
        <button type="submit" className="rounded-lg bg-blue-600 px-4 py-2 text-white hover:bg-blue-700">
          Register
        </button>
      </form>
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm text-slate-300">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Format</th>
              <th className="px-4 py-3">OS</th>
              <th className="px-4 py-3">Path</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-700">
            {(images || []).map((img: any) => (
              <tr key={img.id} className="hover:bg-slate-800/50">
                <td className="px-4 py-3 font-mono text-xs">{img.id}</td>
                <td className="px-4 py-3">{img.name}</td>
                <td className="px-4 py-3">{img.format}</td>
                <td className="px-4 py-3">{img.os_type}</td>
                <td className="px-4 py-3 text-xs font-mono">{img.path}</td>
                <td className="px-4 py-3">
                  <button onClick={() => deleteImage.mutate(img.id)}
                    className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700">Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
