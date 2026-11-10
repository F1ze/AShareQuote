package service

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"quotes/internal/domain"
)

// 通达信的历史起点在报文中只有 16 位；必须在转为 uint16 前检查，避免溢出后重新下载最新页。
const maxBarOffset = domain.MaxHistoricOffset

var exchangeTime = time.FixedZone("Asia/Shanghai", 8*60*60)

// dateRange 沿用领域统一的包含首尾日期规则，分页器不另定义一份时区/结束日语义。
func dateRange(options DownloadOptions) (time.Time, time.Time, error) {
	return domain.DownloadDateRange(options, time.Now())
}

// fetchBars 从最新行情向过去翻页，最后统一排序、去重。
// 这里只收集数据，不写文件；任一页失败就返回错误，避免将部分结果覆盖到已有归档。
// 来源耗尽或触及协议边界时可以返回已取得的数据，但必须同时返回覆盖警告。
func (t *jobRun) fetchBars(client Client, security Security) ([]Bar, string, error) {
	return t.fetchBarsWithOptions(client, security, t.options)
}

// fetchBarsWithOptions 返回（升序去重行情、覆盖警告、致命错误）。
// 流程：确定模式与边界 → 循环请求旧页 → 日期过滤去重 → 检查停止条件 → 排序后返回。
// 有 error 时整批不保存；只有 warning 时允许保存所得记录，并把“不足以证明覆盖”交给界面展示。
func (t *jobRun) fetchBarsWithOptions(client Client, security Security, options DownloadOptions) ([]Bar, string, error) {
	// 第 1 步：日期模式转换为左闭右开范围；旧版模式则使用偏移和剩余目标条数。
	ranged := options.StartDate != ""
	var from, until time.Time
	if ranged {
		var err error
		from, until, err = dateRange(options)
		if err != nil {
			return nil, "", err
		}
	}
	start := options.Start     // 当前页面相对最新 K 线的历史偏移，不是日期或已经保留的行数。
	remaining := options.Count // 仅旧版模式递减；日期模式一直翻到跨过开始日或触及来源边界。
	fromKey, untilKey := from.Format("2006-01-02"), until.Format("2006-01-02")
	unique := make(map[string]Bar) // 市场日期时分→唯一行情，跨页重叠也只保留一根。
	newest := ""                   // 本轮访问到的源端最新时间；与 oldest 一起用于诊断空区间。
	oldest := ""                   // 上一页最早时间，用于检测“翻页却没有向过去推进”的重复响应。
	warning := ""
	for {
		// 第 2 步：先处理取消和偏移边界，再计算本页合法请求量、读取一整页。
		if err := t.ctx.Err(); err != nil {
			return nil, "", err
		}
		if start > maxBarOffset {
			warning = "已达到 16 位历史偏移边界（最多访问 65,536 根 K 线），继续分页会发生位置回绕，已停止"
			break
		}
		// 800 是单次请求上限，不是整个任务上限；最后一页还要受 16 位位置边界约束。
		count := min(800, maxBarOffset+1-start)
		if !ranged {
			count = min(count, remaining)
		}
		page, err := client.SecurityBars(options.Category, security.Market, security.Code, start, count)
		if err != nil {
			client.Close() // 解码失败也丢弃连接，不能继续复用可能已经错位的报文流。
			return nil, "", fmt.Errorf("读取历史行情：%w", err)
		}
		t.mu.Lock()
		t.pages++
		t.mu.Unlock()
		if len(page) == 0 {
			warning = "行情源返回空页，未提供更早记录（可能受历史保留范围或上市时间影响）"
			break
		}
		// 第 3 步：不假设页面内部已排序，扫描最早时间，同时只把目标区间记录放入去重表。
		pageOldest := page[0].Date.Format("2006-01-02 15:04")
		for _, bar := range page {
			key := bar.Date.Format("2006-01-02 15:04")
			if key > newest {
				newest = key
			}
			if key < pageOldest {
				pageOldest = key
			}
			// 上游时间已使用市场时区；固定宽度的年月日时分可按字典序筛选，起点包含、终点排除。
			if ranged && (key < fromKey || key >= untilKey) {
				continue
			}
			if _, exists := unique[key]; !exists {
				unique[key] = bar // 分页重叠时保留先收到的较新页面中的记录。
			}
		}
		// 第 4 步：依次检查重复页、日期起点、旧版目标条数，均未触发才前移历史偏移。
		// 偏移增加却没有拿到更早记录，说明服务端重复分页；继续请求会造成死循环。
		if oldest != "" && pageOldest >= oldest {
			warning = "行情源重复返回相同历史，已停止翻页；请核对实际日期覆盖"
			break
		}
		oldest = pageOldest
		// 分钟线翻到开始日的下午还不够，上午记录可能仍在下一页。
		// 只有最早记录已到开始日零点或更早，才能确认该日期的边界已跨过。
		if ranged && pageOldest <= fromKey+" 00:00" {
			break
		}
		if !ranged {
			remaining -= len(page)
			if remaining <= 0 {
				break
			}
		}
		// 部分服务器每页不足请求条数，必须按实际返回量移动，不能直接加 800 而跳过记录。
		start += len(page)
	}
	// 第 5 步：map 无序，统一升序后交给归档；完全没有目标记录则报错以保留已有文件。
	bars := make([]Bar, 0, len(unique))
	for _, bar := range unique {
		bars = append(bars, bar)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date.Before(bars[j].Date) })
	if warning != "" && ranged {
		warning = historyCoverageWarning(options, warning, oldest, newest, len(bars))
	}
	if len(bars) == 0 {
		if warning != "" {
			return nil, "", fmt.Errorf("%s；所选时段无记录，原有文件已保留", warning)
		}
		return nil, "", errors.New("所选时段没有可用行情；原有文件已保留，请更换日期或检查证券代码")
	}
	return bars, warning, nil
}

// historyCoverageWarning 同时给出请求范围和本轮源端可见范围，避免把“已保存”误读为“三年已取齐”。
// 这里只描述本轮观测，不据此声称源端从未有过更早数据，也不将分时价格伪造为 OHLCV。
func historyCoverageWarning(options DownloadOptions, reason, oldest, newest string, rows int) string {
	observed := "未取得任何源端记录"
	if oldest != "" && newest != "" {
		observed = fmt.Sprintf("本轮源端可见 %s 至 %s", oldest, newest)
	}
	message := fmt.Sprintf("%s；请求 %s 至 %s，%s，所选范围取得 %d 条，未确认完整覆盖", reason, options.StartDate, options.EndDate, observed, rows)
	if options.Category == 7 || options.Category == 8 {
		message += "；多年 1 分钟历史需补录 .lc1/分钟 CSV 或接入可提供该时段的历史源；拆分日期和提高并发不能突破该接口的保留范围"
	}
	return message
}

// incrementalOptions 只在已有归档的最早日期不晚于请求起点时缩短更新窗口。
// 原实现只看最新一条，会把“三年请求、仅存最近几月”直接缩到最近七天，永久跳过早期缺口。
// 最早/最新边界并不证明中间无缺口；历史建库模式仍应扫描全范围。
func (t *jobRun) incrementalOptions(options DownloadOptions, security Security) (DownloadOptions, error) {
	if !options.Incremental || t.archive == nil {
		return options, nil
	}
	bounds, ok := t.archive.(ArchiveBounds)
	if !ok {
		// 不知道历史左边界时保守地保持用户范围，同时保留旧端口的损坏检测。
		_, err := t.archive.Latest(t.ctx, security, options.Category)
		return options, err
	}
	first, latest, err := bounds.Bounds(t.ctx, security, options.Category)
	if err != nil || first.IsZero() || latest.IsZero() {
		return options, err
	}
	if first.In(exchangeTime).Format("2006-01-02") > options.StartDate {
		return options, nil
	}
	overlap := latest.In(exchangeTime).AddDate(0, 0, -7).Format("2006-01-02")
	if overlap > options.StartDate && overlap <= options.EndDate {
		options.StartDate = overlap
	}
	return options, nil
}
