package conc

import "context"

func Run(ctx context.Context , jobs <- chan string) []string{
	var out []string
	for{
		select{
		case<-ctx.Done():
			return out
		case job,ok:= <-jobs:
			if !ok{
				return out
			}
			out=append(out,job)
		}  
	}
}